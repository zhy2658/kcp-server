package main

import (
	"context"
	"fmt"
	"log"

	"game-server/internal/component"
	"game-server/protocol"

	"github.com/topfreegames/pitaya/v2"
	"github.com/topfreegames/pitaya/v2/logger"
)

// 诊断包装器 - 在 Move 方法前后添加日志
type DiagnosticRoom struct {
	*component.Room
	app pitaya.Pitaya
}

func (d *DiagnosticRoom) Move(ctx context.Context, req *protocol.MoveRequest) {
	s := d.app.GetSessionFromCtx(ctx)

	// 诊断信息
	uid := s.UID()
	sessionID := s.ID()
	roomIDVal := s.Get("roomID")

	logger.Log.Infof("=== MOVE DIAGNOSTIC ===")
	logger.Log.Infof("Session ID: %d", sessionID)
	logger.Log.Infof("Session UID: '%s'", uid)
	logger.Log.Infof("RoomID from session: %v", roomIDVal)
	logger.Log.Infof("Requested position: (%.1f, %.1f, %.1f)", req.Position.X, req.Position.Y, req.Position.Z)

	if uid == "" {
		logger.Log.Errorf("❌ UID is empty! Move will be rejected.")
	}

	if roomIDVal == nil {
		logger.Log.Errorf("❌ RoomID is nil! Move will be rejected.")
	}

	// 调用原始方法
	d.Room.Move(ctx, req)

	logger.Log.Infof("=== END DIAGNOSTIC ===")
}

func main() {
	fmt.Println("This is a diagnostic wrapper. Add it to the server code to debug Move issues.")
	fmt.Println("\nThe problem is:")
	fmt.Println("- s.UID() returns empty string in Move handler")
	fmt.Println("- Even though s.Bind() was called in Join handler")
	fmt.Println("\nPossible causes:")
	fmt.Println("1. Session Bind failed silently")
	fmt.Println("2. Session was recreated after Join")
	fmt.Println("3. UID binding is not persistent across requests")
}
