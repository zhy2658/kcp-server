package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"game-server/protocol"

	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

var (
	name = flag.String("name", "Bot2", "Player Name")
	room = flag.String("room", "lobby", "Room ID to join")
	addr = flag.String("addr", "127.0.0.1:3250", "Server address")

	reqID uint = 1

	joinSuccessChan  = make(chan bool, 1)
	createNeededChan = make(chan bool, 1)
	joinRetryChan    = make(chan bool, 1)

	pendingRequests = make(map[uint]string)
)

type BotState int

const (
	StateIdle    BotState = iota // standing still, looking around
	StateWalking                 // walking at normal speed
	StateRunning                 // running fast
	StatePausing                 // brief pause mid-walk (like checking surroundings)
)

type Bot struct {
	SpawnPos  protocol.Vector3
	Pos       protocol.Vector3
	TargetPos protocol.Vector3
	YawDeg    float64 // current facing yaw in degrees
	State     BotState
	Speed     float32 // current normalized speed 0~1

	stateTimer    float64 // seconds remaining in current state
	wanderRadius  float64
	walkSpeed     float64 // units/sec
	runSpeed      float64 // units/sec
	turnSpeed     float64 // degrees/sec for smooth turning
	idleMinSec    float64
	idleMaxSec    float64
	walkMinSec    float64
	walkMaxSec    float64
	pauseMinSec   float64
	pauseMaxSec   float64
	runChance     float64 // probability of running vs walking
}

func NewBot() *Bot {
	b := &Bot{
		SpawnPos:     protocol.Vector3{X: 76.5, Y: 10.2, Z: -54.0},
		wanderRadius: 15.0,
		walkSpeed:    2.5,
		runSpeed:     5.5,
		turnSpeed:    180.0,
		idleMinSec:   1.5,
		idleMaxSec:   5.0,
		walkMinSec:   3.0,
		walkMaxSec:   8.0,
		pauseMinSec:  0.5,
		pauseMaxSec:  2.0,
		runChance:    0.25,
	}
	b.Pos = b.SpawnPos
	b.TargetPos = b.SpawnPos
	b.State = StateIdle
	b.stateTimer = randRange(b.idleMinSec, b.idleMaxSec)
	return b
}

func (b *Bot) pickNewTarget() {
	angle := rand.Float64() * 2 * math.Pi
	r := rand.Float64() * b.wanderRadius
	b.TargetPos = protocol.Vector3{
		X: b.SpawnPos.X + float32(r*math.Cos(angle)),
		Y: b.SpawnPos.Y,
		Z: b.SpawnPos.Z + float32(r*math.Sin(angle)),
	}
}

func (b *Bot) transitionTo(state BotState) {
	b.State = state
	switch state {
	case StateIdle:
		b.stateTimer = randRange(b.idleMinSec, b.idleMaxSec)
		b.Speed = 0
	case StateWalking:
		b.stateTimer = randRange(b.walkMinSec, b.walkMaxSec)
		b.pickNewTarget()
	case StateRunning:
		b.stateTimer = randRange(b.walkMinSec*0.5, b.walkMaxSec*0.5)
		b.pickNewTarget()
	case StatePausing:
		b.stateTimer = randRange(b.pauseMinSec, b.pauseMaxSec)
		b.Speed = 0
	}
}

// Update ticks the bot AI. dt = seconds since last tick.
// Returns true if the bot moved and should send a network update.
func (b *Bot) Update(dt float64) bool {
	b.stateTimer -= dt
	shouldSend := false

	switch b.State {
	case StateIdle:
		b.Speed = lerp32(b.Speed, 0, float32(dt*5))
		if b.stateTimer <= 0 {
			if rand.Float64() < b.runChance {
				b.transitionTo(StateRunning)
			} else {
				b.transitionTo(StateWalking)
			}
		}
		// Still send periodic updates so server knows we're alive
		shouldSend = true

	case StateWalking, StateRunning:
		var moveSpeed float64
		var targetNormSpeed float32
		if b.State == StateRunning {
			moveSpeed = b.runSpeed
			targetNormSpeed = 1.0
		} else {
			moveSpeed = b.walkSpeed
			targetNormSpeed = 0.4
		}

		b.Speed = lerp32(b.Speed, targetNormSpeed, float32(dt*4))

		dx := float64(b.TargetPos.X - b.Pos.X)
		dz := float64(b.TargetPos.Z - b.Pos.Z)
		dist := math.Sqrt(dx*dx + dz*dz)

		if dist < 0.5 {
			// Reached target
			if b.stateTimer <= 0 || rand.Float64() < 0.3 {
				b.transitionTo(StatePausing)
			} else {
				b.pickNewTarget()
			}
		} else {
			// Turn towards target
			targetYaw := math.Atan2(dx, dz) * (180.0 / math.Pi)
			b.YawDeg = smoothDampAngle(b.YawDeg, targetYaw, b.turnSpeed*dt)

			// Move forward
			yawRad := b.YawDeg * (math.Pi / 180.0)
			step := moveSpeed * dt
			b.Pos.X += float32(math.Sin(yawRad) * step)
			b.Pos.Z += float32(math.Cos(yawRad) * step)
		}

		if b.stateTimer <= 0 {
			b.transitionTo(StatePausing)
		}
		shouldSend = true

	case StatePausing:
		b.Speed = lerp32(b.Speed, 0, float32(dt*6))
		if b.stateTimer <= 0 {
			if rand.Float64() < 0.15 {
				b.transitionTo(StateIdle)
			} else if rand.Float64() < b.runChance {
				b.transitionTo(StateRunning)
			} else {
				b.transitionTo(StateWalking)
			}
		}
		shouldSend = true
	}

	return shouldSend
}

func (b *Bot) BuildMoveRequest() *protocol.MoveRequest {
	return &protocol.MoveRequest{
		Position:   &protocol.Vector3{X: b.Pos.X, Y: b.Pos.Y, Z: b.Pos.Z},
		Rotation:   yawToQuaternion(b.YawDeg),
		Speed:      b.Speed,
		IsGrounded: true,
	}
}

// --- Math helpers ---

func yawToQuaternion(yawDeg float64) *protocol.Quaternion {
	halfRad := (yawDeg * math.Pi / 180.0) / 2.0
	return &protocol.Quaternion{
		X: 0,
		Y: float32(math.Sin(halfRad)),
		Z: 0,
		W: float32(math.Cos(halfRad)),
	}
}

func smoothDampAngle(current, target, maxStep float64) float64 {
	diff := target - current
	for diff > 180 {
		diff -= 360
	}
	for diff < -180 {
		diff += 360
	}
	if math.Abs(diff) <= maxStep {
		return target
	}
	if diff > 0 {
		return current + maxStep
	}
	return current - maxStep
}

func lerp32(a, b, t float32) float32 {
	if t > 1 {
		t = 1
	}
	return a + (b-a)*t
}

func randRange(min, max float64) float64 {
	return min + rand.Float64()*(max-min)
}

// --- Network / main ---

func main() {
	flag.Parse()
	log.SetPrefix(fmt.Sprintf("[%s] ", *name))

	conn, err := kcp.DialWithOptions(*addr, nil, 0, 0)
	if err != nil {
		log.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetStreamMode(true)
	conn.SetWindowSize(128, 128)
	conn.SetACKNoDelay(true)

	if err := handshake(conn); err != nil {
		log.Fatalf("Handshake failed: %v", err)
	}
	log.Println("Connected!")

	go readLoop(conn)

	log.Printf("Joining room %s...", *room)
	joinReq := &protocol.JoinRequest{RoomId: *room, Name: *name}
	data, _ := proto.Marshal(joinReq)
	sendRequest(conn, "room.join", data)

	joined := make(chan bool)
	go func() {
		for {
			select {
			case <-joinSuccessChan:
				joined <- true
				return
			case <-createNeededChan:
				log.Printf("Room not found. Creating room %s...", *room)
				req := &protocol.CreateRoomRequest{Name: *room, MaxPlayers: 10}
				d, _ := proto.Marshal(req)
				sendRequest(conn, "room.create", d)
			case <-joinRetryChan:
				log.Printf("Retrying join room %s...", *room)
				d, _ := proto.Marshal(joinReq)
				sendRequest(conn, "room.join", d)
			}
		}
	}()

	<-joined
	log.Println("Join complete. Starting bot AI.")

	runBotBehavior(conn)
}

func runBotBehavior(conn *kcp.UDPSession) {
	const tickRate = 100 * time.Millisecond // 10 Hz
	ticker := time.NewTicker(tickRate)
	defer ticker.Stop()

	bot := NewBot()
	dt := tickRate.Seconds()

	// Only send network updates every N ticks (idle sends less frequently)
	sendCounter := 0

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			moved := bot.Update(dt)
			sendCounter++

			shouldSend := false
			if bot.State == StateIdle || bot.State == StatePausing {
				shouldSend = moved && sendCounter%5 == 0 // every 500ms when idle
			} else {
				shouldSend = moved // every 100ms when moving
			}

			if shouldSend {
				req := bot.BuildMoveRequest()
				d, _ := proto.Marshal(req)
				sendNotify(conn, "room.move", d)
			}

		case <-sigCh:
			log.Println("Bot stopping...")
			return
		}
	}
}

// --- Networking (same protocol as main bot) ---

func handshake(conn *kcp.UDPSession) error {
	encoder := codec.NewPomeloPacketEncoder()
	decoder := codec.NewPomeloPacketDecoder()
	handshakeData := &session.HandshakeClientData{
		Platform:    "bot",
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
	ack, _ := encoder.Encode(packet.HandshakeAck, []byte{})
	conn.Write(ack)
	return nil
}

func sendRequest(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{Type: message.Request, ID: reqID, Route: route, Data: data}
	pendingRequests[reqID] = route
	reqID++
	send(conn, msg)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{Type: message.Notify, Route: route, Data: data}
	send(conn, msg)
}

func send(conn *kcp.UDPSession, msg *message.Message) {
	msgEncoder := message.NewMessagesEncoder(false)
	encodedMsg, err := msgEncoder.Encode(msg)
	if err != nil {
		log.Printf("Encode msg error: %v", err)
		return
	}
	pktEncoder := codec.NewPomeloPacketEncoder()
	packetData, err := pktEncoder.Encode(packet.Data, encodedMsg)
	if err != nil {
		log.Printf("Encode pkt error: %v", err)
		return
	}
	conn.Write(packetData)
}

func readLoop(conn *kcp.UDPSession) {
	decoder := codec.NewPomeloPacketDecoder()
	header := make([]byte, codec.HeadLength)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		size, _, err := codec.ParseHeader(header)
		if err != nil {
			continue
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		packets, err := decoder.Decode(append(header, body...))
		if err != nil {
			continue
		}
		for _, p := range packets {
			if p.Type == packet.Data {
				msg, err := message.Decode(p.Data)
				if err != nil {
					continue
				}
				handleMessage(msg)
			} else if p.Type == packet.Heartbeat {
				pktEncoder := codec.NewPomeloPacketEncoder()
				ack, _ := pktEncoder.Encode(packet.Heartbeat, nil)
				conn.Write(ack)
			}
		}
	}
}

func handleMessage(msg *message.Message) {
	if msg.Type == message.Response {
		route, ok := pendingRequests[msg.ID]
		if ok {
			delete(pendingRequests, msg.ID)
		}

		var pitayaErr struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(msg.Data, &pitayaErr) == nil && pitayaErr.Code != "" {
			if route == "room.join" {
				if pitayaErr.Code == "PIT-404" || strings.Contains(pitayaErr.Msg, "room not found") {
					createNeededChan <- true
				} else {
					log.Fatalf("Join failed: %s - %s", pitayaErr.Code, pitayaErr.Msg)
				}
			}
			return
		}

		if route == "room.create" {
			createResp := new(protocol.CreateRoomResponse)
			if err := proto.Unmarshal(msg.Data, createResp); err == nil && createResp.Id != "" {
				log.Printf(">> Room Created: %s", createResp.Name)
				joinRetryChan <- true
			}
		} else if route == "room.join" {
			joinResp := new(protocol.JoinResponse)
			if err := proto.Unmarshal(msg.Data, joinResp); err == nil {
				if joinResp.Code == 200 {
					log.Printf(">> Joined Room: %s", joinResp.RoomId)
					joinSuccessChan <- true
				} else if joinResp.Code == 404 || strings.Contains(joinResp.Message, "room not found") {
					createNeededChan <- true
				}
			}
		}
	}
}
