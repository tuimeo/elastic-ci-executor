package azure

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance/v2"
	"github.com/gorilla/websocket"
	"github.com/tuimeo/elastic-ci-executor/internal/shell"
)

// execCommandViaAPI executes a command using Azure ACI ExecuteCommand API + WebSocket
func (p *ACIProvider) execCommandViaAPI(ctx context.Context, resourceGroup, containerGroupName, containerName string, script []byte, stdout io.Writer) (int, error) {
	// Detect best available shell and execute the script inline
	sentinel := fmt.Sprintf("__ELASTIC_CI_EXIT_%d__", time.Now().UnixNano())
	wrappedCommand := shell.WrapInlineScript(shellQuote(string(script)), sentinel)

	cols := int32(200)
	rows := int32(60)

	// Call the ExecuteCommand API
	response, err := p.containerClient.ExecuteCommand(
		ctx,
		resourceGroup,
		containerGroupName,
		containerName,
		armcontainerinstance.ContainerExecRequest{
			Command: &wrappedCommand,
			TerminalSize: &armcontainerinstance.ContainerExecRequestTerminalSize{
				Cols: &cols,
				Rows: &rows,
			},
		},
		nil,
	)
	if err != nil {
		return 1, fmt.Errorf("failed to call ExecuteCommand API: %w", err)
	}

	if response.WebSocketURI == nil || *response.WebSocketURI == "" {
		return 1, fmt.Errorf("ExecuteCommand returned empty WebSocket URI")
	}
	if response.Password == nil || *response.Password == "" {
		return 1, fmt.Errorf("ExecuteCommand returned empty password")
	}

	// Connect to WebSocket and stream output
	exitCode, err := p.streamWebSocket(ctx, *response.WebSocketURI, *response.Password, sentinel, stdout)
	if err != nil {
		return 1, fmt.Errorf("WebSocket execution failed: %w", err)
	}

	return exitCode, nil
}

// streamWebSocket connects to the ACI WebSocket, authenticates, and streams output
func (p *ACIProvider) streamWebSocket(ctx context.Context, wsURI, password, sentinel string, stdout io.Writer) (int, error) {
	u, err := url.Parse(wsURI)
	if err != nil {
		return 1, fmt.Errorf("invalid WebSocket URI: %w", err)
	}

	// Ensure correct WebSocket scheme
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

	// Azure ACI requires sending the password as the first message to authenticate
	if err := conn.WriteMessage(websocket.TextMessage, []byte(password)); err != nil {
		return 1, fmt.Errorf("failed to send authentication: %w", err)
	}

	// Start a ping goroutine to keep the WebSocket alive.
	// Azure ACI's server-side idle timeout (~5min) will close connections with no traffic.
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if writeErr := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); writeErr != nil {
					return
				}
			case <-pingDone:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	defer close(pingDone)

	// Read messages until connection closes
	exitCode := 1
	foundExit := false

	for {
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				break
			}
			if websocket.IsUnexpectedCloseError(err) {
				break
			}
			break
		}

		text := string(message)

		// Check for exit code sentinel
		if idx := strings.Index(text, sentinel); idx != -1 {
			foundExit = true
			after := strings.TrimSpace(text[idx+len(sentinel):])
			if _, err := fmt.Sscanf(after, "%d", &exitCode); err != nil {
				exitCode = 1
			}

			before := text[:idx]
			if len(before) > 0 {
				_, _ = stdout.Write([]byte(before))
			}
			continue
		}

		if !foundExit {
			_, _ = stdout.Write(message)
		}
	}

	if !foundExit {
		return 1, fmt.Errorf("command output did not contain exit code sentinel")
	}

	return exitCode, nil
}

// shellQuote wraps a string in single quotes for safe shell execution
func shellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}
