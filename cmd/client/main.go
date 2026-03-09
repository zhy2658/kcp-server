package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"3dtest-server/protocol"

	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

func main() {
	// 1. Connect to KCP server
	conn, err := kcp.DialWithOptions("127.0.0.1:3250", nil, 10, 3)
	if err != nil {
		log.Fatalf("Failed to dial KCP: %v", err)
	}
	defer conn.Close()

	// Configure KCP (must match server settings)
	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetStreamMode(true)
	conn.SetWindowSize(128, 128)
	conn.SetACKNoDelay(true)

	fmt.Println("Connected to KCP server at 127.0.0.1:3250")

	// 2. Perform Handshake
	if err := handshake(conn); err != nil {
		log.Fatalf("Handshake failed: %v", err)
	}
	fmt.Println("Handshake successful!")

	// 3. Start reading loop
	go readLoop(conn)

	// 4. Send Join Request (Request/Response)
	// Route: "room.join"
	joinReq := &protocol.JoinRequest{
		Name: "Player1",
	}
	data, _ := proto.Marshal(joinReq)
	sendRequest(conn, "room.join", data)

	// 5. Wait a bit then send a chat message (Notify)
	time.Sleep(1 * time.Second)
	chatMsg := &protocol.ChatMessage{
		Content: "Hello KCP World!",
	}
	data, _ = proto.Marshal(chatMsg)
	// Route: "room.message" is a Notify handler (returns error only)
	sendNotify(conn, "room.message", data)

	// Keep running
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}

func handshake(conn *kcp.UDPSession) error {
	encoder := codec.NewPomeloPacketEncoder()
	decoder := codec.NewPomeloPacketDecoder()

	// Send Handshake
	handshakeData := &session.HandshakeClientData{
		Platform:    "mac",
		LibVersion:  "0.3.5-release",
		BuildNumber: "20",
		Version:     "2.1",
	}
	data, _ := json.Marshal(map[string]interface{}{
		"sys":  handshakeData,
		"user": map[string]interface{}{},
	})

	pkt, err := encoder.Encode(packet.Handshake, data)
	if err != nil {
		return err
	}
	conn.Write(pkt)

	// Read Handshake Response
	// We need to read enough bytes for header
	header := make([]byte, codec.HeadLength)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	size, _, err := codec.ParseHeader(header)
	if err != nil {
		return err
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}

	packets, err := decoder.Decode(append(header, body...))
	if err != nil {
		return err
	}

	if len(packets) == 0 || packets[0].Type != packet.Handshake {
		return fmt.Errorf("expected handshake packet")
	}

	// Send Handshake ACK
	ack, _ := encoder.Encode(packet.HandshakeAck, []byte{})
	conn.Write(ack)

	return nil
}

var reqID uint = 1

func sendRequest(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{
		Type:  message.Request,
		ID:    reqID,
		Route: route,
		Data:  data,
	}
	reqID++
	send(conn, msg)
	fmt.Printf("Sent Request: %s (ID: %d)\n", route, msg.ID)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{
		Type:  message.Notify,
		Route: route,
		Data:  data,
	}
	send(conn, msg)
	fmt.Printf("Sent Notify: %s\n", route)
}

func send(conn *kcp.UDPSession, msg *message.Message) {
	// Encode Message
	msgEncoder := message.NewMessagesEncoder(false)
	encodedMsg, err := msgEncoder.Encode(msg)
	if err != nil {
		log.Printf("Failed to encode message: %v", err)
		return
	}

	// Encode Packet
	pktEncoder := codec.NewPomeloPacketEncoder()
	packetData, err := pktEncoder.Encode(packet.Data, encodedMsg)
	if err != nil {
		log.Printf("Failed to encode packet: %v", err)
		return
	}

	conn.Write(packetData)
}

func readLoop(conn *kcp.UDPSession) {
	decoder := codec.NewPomeloPacketDecoder()
	header := make([]byte, codec.HeadLength)

	for {
		// Read Header
		if _, err := io.ReadFull(conn, header); err != nil {
			log.Printf("Read error: %v", err)
			return
		}

		// Parse Header
		size, _, err := codec.ParseHeader(header)
		if err != nil {
			log.Printf("Header parse error: %v", err)
			continue
		}

		// Read Body
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			log.Printf("Body read error: %v", err)
			return
		}

		// Decode Packet
		packets, err := decoder.Decode(append(header, body...))
		if err != nil {
			log.Printf("Packet decode error: %v", err)
			continue
		}

		for _, p := range packets {
			if p.Type == packet.Data {
				msg, err := message.Decode(p.Data)
				if err != nil {
					log.Printf("Message decode error: %v", err)
					continue
				}
				handleMessage(msg)
			} else if p.Type == packet.Heartbeat {
				// Reply to heartbeat
				// ...
			}
		}
	}
}

func handleMessage(msg *message.Message) {
	if msg.Type == message.Response {
		fmt.Printf("Received Response (ID: %d)\n", msg.ID)
		// Decode payload based on ID or context (simplified)
		// For Join (ID 1), it's JoinResponse
		if msg.ID == 1 {
			resp := &protocol.JoinResponse{}
			proto.Unmarshal(msg.Data, resp)
			fmt.Printf("Join Response: Code=%d, Msg=%s\n", resp.Code, resp.Message)
		}
	} else if msg.Type == message.Push {
		fmt.Printf("Received Push (Route: %s)\n", msg.Route)
		if msg.Route == "OnMessage" {
			chat := &protocol.ChatMessage{}
			proto.Unmarshal(msg.Data, chat)
			fmt.Printf("Chat Push: User %d says: %s\n", chat.SenderId, chat.Content)
		}
	}
}
