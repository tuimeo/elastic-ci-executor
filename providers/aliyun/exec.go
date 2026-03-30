package aliyun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	eciclient "github.com/alibabacloud-go/eci-20180808/v3/client"
	"github.com/gorilla/websocket"
	"github.com/tuimeo/elastic-ci-executor/internal/logger"
	"github.com/tuimeo/elastic-ci-executor/internal/shell"
)

const (
	// maxCommandLen is the ECI API limit for the Command parameter.
	// We use a conservative value to leave room for shell wrapping.
	maxChunkLen    = 1400
	scriptTempPath = "/tmp/_ci_script.sh"
)

// execCommandViaAPI executes a script in a container using ECI ExecContainerCommand API.
// For scripts that exceed ECI's 2048-char command limit, it first writes the script
// to a temp file inside the container (in base64 chunks), then executes the file.
func (p *ECIProvider) execCommandViaAPI(ctx context.Context, containerGroupId, containerName string, script []byte, stdout io.Writer) (int, error) {
	// Step 1: Write script to a temp file inside the container
	if err := p.writeScriptToContainer(ctx, containerGroupId, containerName, script); err != nil {
		return 1, fmt.Errorf("failed to write script to container: %w", err)
	}

	// Step 2: Detect best available shell and execute the script file
	sentinel := fmt.Sprintf("__ELASTIC_CI_EXIT_%d__", time.Now().UnixNano())
	execCmd := shell.WrapScriptFile(scriptTempPath, sentinel)

	return p.execShellCommand(ctx, containerGroupId, containerName, execCmd, sentinel, stdout)
}

// writeScriptToContainer writes script content to a temp file inside the container
// using base64-encoded chunks to stay within ECI's command length limit.
func (p *ECIProvider) writeScriptToContainer(ctx context.Context, containerGroupId, containerName string, script []byte) error {
	encoded := base64.StdEncoding.EncodeToString(script)

	// Split base64 string into chunks and append to a temp file
	for i := 0; i < len(encoded); i += maxChunkLen {
		end := i + maxChunkLen
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]

		// Use >> to append each chunk
		cmd := fmt.Sprintf("printf '%%s' '%s' >> %s.b64", chunk, scriptTempPath)
		if err := p.execSimpleCommand(ctx, containerGroupId, containerName, cmd); err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", i/maxChunkLen, err)
		}
	}

	// Decode the base64 file into the actual script
	cmd := fmt.Sprintf("base64 -d < %s.b64 > %s && rm %s.b64", scriptTempPath, scriptTempPath, scriptTempPath)
	if err := p.execSimpleCommand(ctx, containerGroupId, containerName, cmd); err != nil {
		return fmt.Errorf("failed to decode script: %w", err)
	}

	logger.Debug("script written to container", "path", scriptTempPath, "size", len(script), "chunks", (len(encoded)+maxChunkLen-1)/maxChunkLen)
	return nil
}

// execSimpleCommand runs a short command without streaming output (fire-and-forget with exit check).
func (p *ECIProvider) execSimpleCommand(ctx context.Context, containerGroupId, containerName, shellCmd string) error {
	cmdArray := []string{"/bin/sh", "-c", shellCmd}
	cmdJSON, err := json.Marshal(cmdArray)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}
	cmdStr := string(cmdJSON)

	request := &eciclient.ExecContainerCommandRequest{
		RegionId:         &p.cfg.Region,
		ContainerGroupId: &containerGroupId,
		ContainerName:    &containerName,
		Command:          &cmdStr,
		TTY:              boolPtr(false),
	}

	resp, err := p.eciClient.ExecContainerCommand(request)
	if err != nil {
		return fmt.Errorf("ExecContainerCommand failed: %w", err)
	}

	if resp.Body == nil || resp.Body.WebSocketUri == nil || *resp.Body.WebSocketUri == "" {
		return fmt.Errorf("empty WebSocket URI")
	}

	// Connect and wait for the command to finish (read until close)
	return p.waitWebSocketDone(ctx, *resp.Body.WebSocketUri)
}

// waitWebSocketDone connects to a WebSocket and reads until it closes.
func (p *ECIProvider) waitWebSocketDone(ctx context.Context, wsURI string) error {
	u, err := url.Parse(wsURI)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URI: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Read until closed
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, _, err := conn.ReadMessage()
		if err != nil {
			return nil // closed or timeout = done
		}
	}
}

// execShellCommand runs a shell command and streams output, looking for the exit sentinel.
func (p *ECIProvider) execShellCommand(ctx context.Context, containerGroupId, containerName, shellCmd, sentinel string, stdout io.Writer) (int, error) {
	cmdArray := []string{"/bin/sh", "-c", shellCmd}
	cmdJSON, err := json.Marshal(cmdArray)
	if err != nil {
		return 1, fmt.Errorf("failed to marshal command: %w", err)
	}
	cmdStr := string(cmdJSON)

	logger.Debug("exec command", "container", containerGroupId, "target", containerName)

	request := &eciclient.ExecContainerCommandRequest{
		RegionId:         &p.cfg.Region,
		ContainerGroupId: &containerGroupId,
		ContainerName:    &containerName,
		Command:          &cmdStr,
		TTY:              boolPtr(false),
	}

	response, err := p.eciClient.ExecContainerCommand(request)
	if err != nil {
		return 1, fmt.Errorf("failed to call ExecContainerCommand API: %w", err)
	}

	if response.Body == nil || response.Body.WebSocketUri == nil || *response.Body.WebSocketUri == "" {
		return 1, fmt.Errorf("ExecContainerCommand returned empty WebSocket URI")
	}

	return p.streamWebSocket(ctx, *response.Body.WebSocketUri, sentinel, stdout)
}

// streamWebSocket connects to the ECI WebSocket URI and streams output
func (p *ECIProvider) streamWebSocket(ctx context.Context, wsURI, sentinel string, stdout io.Writer) (int, error) {
	u, err := url.Parse(wsURI)
	if err != nil {
		return 1, fmt.Errorf("invalid WebSocket URI: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return 1, fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	logger.Debug("WebSocket connected for streaming")

	exitCode := 1
	foundExit := false
	buf := &strings.Builder{}

	for {
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		msgType, message, err := conn.ReadMessage()
		if err != nil {
			switch {
			case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway):
				logger.Debug("WebSocket closed normally")
			case strings.Contains(err.Error(), "timeout"):
				logger.Debug("WebSocket read timeout, checking buffer")
			default:
				logger.Debug("WebSocket read error", "error", err)
			}
			break
		}

		logger.Debug("received WebSocket message", "type", msgType, "len", len(message))

		if len(message) == 0 {
			continue
		}

		decoded := tryBase64Decode(message)
		text := string(decoded)

		buf.WriteString(text)
		fullOutput := buf.String()

		if idx := strings.Index(fullOutput, sentinel); idx != -1 {
			foundExit = true
			after := fullOutput[idx+len(sentinel):]
			after = strings.TrimSpace(after)
			var code int
			if _, err := fmt.Sscanf(after, "%d", &code); err == nil {
				exitCode = code
			}
			before := fullOutput[:idx]
			if len(before) > 0 {
				_, _ = stdout.Write([]byte(before))
			}
			continue
		}

		if !foundExit {
			_, _ = stdout.Write(decoded)
			buf.Reset()
		}
	}

	// Check remaining buffer
	if !foundExit {
		remaining := buf.String()
		if idx := strings.Index(remaining, sentinel); idx != -1 {
			foundExit = true
			after := remaining[idx+len(sentinel):]
			after = strings.TrimSpace(after)
			var code int
			if _, err := fmt.Sscanf(after, "%d", &code); err == nil {
				exitCode = code
			}
			before := remaining[:idx]
			if len(before) > 0 {
				_, _ = stdout.Write([]byte(before))
			}
		} else if len(remaining) > 0 {
			_, _ = stdout.Write([]byte(remaining))
		}
	}

	if !foundExit {
		logger.Debug("sentinel not found, assuming success")
		return 0, nil
	}

	return exitCode, nil
}

// tryBase64Decode attempts to base64-decode data, returns original if not valid base64
func tryBase64Decode(data []byte) []byte {
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return data
	}
	return decoded
}

func boolPtr(b bool) *bool { return &b }
