package aws

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/gorilla/websocket"
	"github.com/tuimeo/elastic-ci-executor/internal/shell"
)

// execCommandViaAPI executes a command using ECS ExecuteCommand API + SSM WebSocket
func (p *FargateProvider) execCommandViaAPI(ctx context.Context, cluster, taskArn, containerName string, script []byte, stdout io.Writer) (int, error) {
	// Detect best available shell and execute the script inline
	sentinel := fmt.Sprintf("__ELASTIC_CI_EXIT_%d__", time.Now().UnixNano())
	wrappedCommand := shell.WrapInlineScript(shellQuote(string(script)), sentinel)

	// Call ECS ExecuteCommand API
	output, err := p.ecsClient.ExecuteCommand(ctx, &ecs.ExecuteCommandInput{
		Cluster:     aws.String(cluster),
		Task:        aws.String(taskArn),
		Container:   aws.String(containerName),
		Command:     aws.String(wrappedCommand),
		Interactive: true,
	})
	if err != nil {
		return 1, fmt.Errorf("failed to call ExecuteCommand API: %w", err)
	}

	if output.Session == nil {
		return 1, fmt.Errorf("ExecuteCommand returned nil session")
	}
	if output.Session.StreamUrl == nil || *output.Session.StreamUrl == "" {
		return 1, fmt.Errorf("ExecuteCommand returned empty stream URL")
	}
	if output.Session.TokenValue == nil || *output.Session.TokenValue == "" {
		return 1, fmt.Errorf("ExecuteCommand returned empty token")
	}

	// Connect to SSM WebSocket and stream output
	exitCode, err := p.streamSSMSession(ctx, *output.Session.StreamUrl, *output.Session.TokenValue, *output.Session.SessionId, sentinel, stdout)
	if err != nil {
		return 1, fmt.Errorf("SSM session failed: %w", err)
	}

	return exitCode, nil
}

// SSM Agent message types
const (
	ssmOutputStreamData = "output_stream_data"
	ssmAcknowledge      = "acknowledge"
	ssmChannelClosed    = "channel_closed"
)

// ssmAgentMessage represents an SSM Session Manager protocol message
type ssmAgentMessage struct {
	HeaderLength   uint32
	MessageType    string
	SchemaVersion  uint32
	CreatedDate    uint64
	SequenceNumber int64
	Flags          uint64
	MessageID      [16]byte
	PayloadDigest  [32]byte
	PayloadType    uint32
	PayloadLength  uint32
	Payload        []byte
}

// ssmOpenDataChannelInput is the initial handshake message
type ssmOpenDataChannelInput struct {
	MessageSchemaVersion string `json:"MessageSchemaVersion"`
	RequestID            string `json:"RequestId"`
	TokenValue           string `json:"TokenValue"`
}

// streamSSMSession connects to the SSM WebSocket, performs the handshake, and streams output
func (p *FargateProvider) streamSSMSession(ctx context.Context, streamURL, tokenValue, sessionID, sentinel string, stdout io.Writer) (int, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, streamURL, nil)
	if err != nil {
		return 1, fmt.Errorf("failed to connect to SSM WebSocket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Step 1: Send the open_data_channel handshake
	openMsg := ssmOpenDataChannelInput{
		MessageSchemaVersion: "1.0",
		RequestID:            sessionID,
		TokenValue:           tokenValue,
	}
	openMsgJSON, err := json.Marshal(openMsg)
	if err != nil {
		return 1, fmt.Errorf("failed to marshal open channel message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, openMsgJSON); err != nil {
		return 1, fmt.Errorf("failed to send open channel message: %w", err)
	}

	// Step 2: Read messages from the WebSocket
	exitCode := 1
	foundExit := false
	var outputBuf strings.Builder
	sequenceNumber := int64(0)

	for {
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		default:
		}

		msgType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				break
			}
			if websocket.IsUnexpectedCloseError(err) {
				break
			}
			break
		}

		// SSM uses binary messages for the agent protocol
		if msgType == websocket.BinaryMessage {
			agentMsg, parseErr := parseSSMMessage(message)
			if parseErr != nil {
				continue
			}

			switch agentMsg.MessageType {
			case ssmOutputStreamData:
				// Send acknowledgement
				p.sendSSMAcknowledge(conn, agentMsg, sequenceNumber)
				sequenceNumber++

				payload := string(agentMsg.Payload)
				outputBuf.WriteString(payload)

				// Check for sentinel in accumulated output
				accumulated := outputBuf.String()
				if idx := strings.Index(accumulated, sentinel); idx != -1 {
					foundExit = true
					after := strings.TrimSpace(accumulated[idx+len(sentinel):])
					if _, err := fmt.Sscanf(after, "%d", &exitCode); err != nil {
						exitCode = 1
					}

					// Write everything before sentinel
					before := accumulated[:idx]
					if len(before) > 0 {
						_, _ = stdout.Write([]byte(before))
					}
					outputBuf.Reset()
				} else if !foundExit {
					// Flush complete lines to stdout, keep partial for sentinel detection
					if lastNewline := strings.LastIndex(accumulated, "\n"); lastNewline >= 0 {
						// Only flush if the buffer is getting large and sentinel hasn't appeared
						if outputBuf.Len() > 4096 {
							_, _ = stdout.Write([]byte(accumulated[:lastNewline+1]))
							outputBuf.Reset()
							outputBuf.WriteString(accumulated[lastNewline+1:])
						}
					}
				}

			case ssmChannelClosed:
				// Flush remaining output
				if !foundExit && outputBuf.Len() > 0 {
					remaining := outputBuf.String()
					if idx := strings.Index(remaining, sentinel); idx != -1 {
						foundExit = true
						after := strings.TrimSpace(remaining[idx+len(sentinel):])
						if _, err := fmt.Sscanf(after, "%d", &exitCode); err != nil {
						exitCode = 1
					}
						before := remaining[:idx]
						if len(before) > 0 {
							_, _ = stdout.Write([]byte(before))
						}
					} else {
						_, _ = stdout.Write([]byte(remaining))
					}
				}
				goto done
			}
		}
	}

done:
	// Flush any remaining buffered output
	if !foundExit && outputBuf.Len() > 0 {
		remaining := outputBuf.String()
		if idx := strings.Index(remaining, sentinel); idx != -1 {
			foundExit = true
			after := strings.TrimSpace(remaining[idx+len(sentinel):])
			if _, err := fmt.Sscanf(after, "%d", &exitCode); err != nil {
				exitCode = 1
			}
			before := remaining[:idx]
			if len(before) > 0 {
				_, _ = stdout.Write([]byte(before))
			}
		} else {
			_, _ = stdout.Write([]byte(remaining))
		}
	}

	if !foundExit {
		return 1, fmt.Errorf("command output did not contain exit code sentinel")
	}

	return exitCode, nil
}

// parseSSMMessage parses a binary SSM Agent protocol message
func parseSSMMessage(data []byte) (*ssmAgentMessage, error) {
	if len(data) < 116 {
		return nil, fmt.Errorf("message too short: %d bytes", len(data))
	}

	msg := &ssmAgentMessage{}

	offset := 0

	// Header length (4 bytes)
	msg.HeaderLength = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Message type (32 bytes, null-padded string)
	msgTypeBytes := data[offset : offset+32]
	msg.MessageType = strings.TrimRight(string(msgTypeBytes), "\x00")
	offset += 32

	// Schema version (4 bytes)
	msg.SchemaVersion = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Created date (8 bytes)
	msg.CreatedDate = binary.BigEndian.Uint64(data[offset : offset+8])
	offset += 8

	// Sequence number (8 bytes)
	msg.SequenceNumber = int64(binary.BigEndian.Uint64(data[offset : offset+8])) //nolint:gosec // G115 - SSM protocol field
	offset += 8

	// Flags (8 bytes)
	msg.Flags = binary.BigEndian.Uint64(data[offset : offset+8])
	offset += 8

	// Message ID (16 bytes)
	copy(msg.MessageID[:], data[offset:offset+16])
	offset += 16

	// Payload digest (32 bytes)
	copy(msg.PayloadDigest[:], data[offset:offset+32])
	offset += 32

	// Payload type (4 bytes)
	msg.PayloadType = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Payload length (4 bytes)
	msg.PayloadLength = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Payload
	if offset+int(msg.PayloadLength) <= len(data) {
		msg.Payload = data[offset : offset+int(msg.PayloadLength)]
	}

	return msg, nil
}

// sendSSMAcknowledge sends an acknowledgement message for a received SSM message
func (p *FargateProvider) sendSSMAcknowledge(conn *websocket.Conn, msg *ssmAgentMessage, seqNum int64) {
	ackContent := map[string]interface{}{
		"AcknowledgedMessageType":           msg.MessageType,
		"AcknowledgedMessageId":             fmt.Sprintf("%x", msg.MessageID),
		"AcknowledgedMessageSequenceNumber": msg.SequenceNumber,
		"IsSequentialMessage":               true,
	}
	ackJSON, err := json.Marshal(ackContent)
	if err != nil {
		return
	}

	ackMsg := buildSSMMessage(ssmAcknowledge, ackJSON, seqNum)
	_ = conn.WriteMessage(websocket.BinaryMessage, ackMsg)
}

// buildSSMMessage creates a binary SSM Agent protocol message
func buildSSMMessage(messageType string, payload []byte, sequenceNumber int64) []byte {
	headerLen := uint32(116)
	payloadLen := uint32(len(payload)) //nolint:gosec // G115 - payload bounded by SSM message size
	totalLen := int(headerLen) + len(payload)

	buf := make([]byte, totalLen)
	offset := 0

	// Header length
	binary.BigEndian.PutUint32(buf[offset:], headerLen)
	offset += 4

	// Message type (32 bytes, null-padded)
	msgTypeBytes := make([]byte, 32)
	copy(msgTypeBytes, messageType)
	copy(buf[offset:], msgTypeBytes)
	offset += 32

	// Schema version
	binary.BigEndian.PutUint32(buf[offset:], 1)
	offset += 4

	// Created date (milliseconds since epoch)
	binary.BigEndian.PutUint64(buf[offset:], uint64(time.Now().UnixMilli())) //nolint:gosec // G115 - timestamp always positive
	offset += 8

	// Sequence number
	binary.BigEndian.PutUint64(buf[offset:], uint64(sequenceNumber)) //nolint:gosec // G115 - sequence number always positive
	offset += 8

	// Flags
	binary.BigEndian.PutUint64(buf[offset:], 0)
	offset += 8

	// Message ID (16 bytes, zeros for ack)
	offset += 16

	// Payload digest (32 bytes, zeros)
	offset += 32

	// Payload type (1 = output)
	binary.BigEndian.PutUint32(buf[offset:], 1)
	offset += 4

	// Payload length
	binary.BigEndian.PutUint32(buf[offset:], payloadLen)
	offset += 4

	// Payload
	copy(buf[offset:], payload)

	return buf
}

// shellQuote wraps a string in single quotes for safe shell execution
func shellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}
