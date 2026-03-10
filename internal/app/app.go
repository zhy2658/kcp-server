package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"3dtest-server/internal/component"
	"3dtest-server/internal/config"
	"3dtest-server/internal/dashboard"
	"3dtest-server/internal/network"
	"3dtest-server/internal/serializer"

	"github.com/sirupsen/logrus"
	"github.com/topfreegames/pitaya/v2"
	pitayacomponent "github.com/topfreegames/pitaya/v2/component"
	pitayaconfig "github.com/topfreegames/pitaya/v2/config"
	"github.com/topfreegames/pitaya/v2/logger"
	logruswrapper "github.com/topfreegames/pitaya/v2/logger/logrus"
	"go.uber.org/fx"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Module provides the main application components
var Module = fx.Module("server",
	fx.Provide(
		config.Load,
		NewLogger,
		NewPitayaBuilder,
		NewPitayaApp,
		component.NewRoom,
	),
	fx.Invoke(
		RegisterComponents,
		StartApp,
		StartDashboard,
	),
)

func NewLogger(cfg *config.Config) logrus.FieldLogger {
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{})
	level, err := logrus.ParseLevel(cfg.Log.Level)
	if err != nil {
		level = logrus.DebugLevel
	}
	l.SetLevel(level)

	fileLogger := &lumberjack.Logger{
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
	}

	if cfg.Dashboard.Enabled {
		// Dashboard enabled: Log only to file to keep console clean
		l.SetOutput(fileLogger)
	} else {
		// Dashboard disabled: Log to both file and stdout
		l.SetOutput(io.MultiWriter(os.Stdout, fileLogger))
	}

	// Set global Pitaya logger as well
	logger.SetLogger(logruswrapper.NewWithFieldLogger(l))

	return l
}

func NewPitayaBuilder(cfg *config.Config, _ logrus.FieldLogger) *pitaya.Builder {
	pitayaConfig := pitayaconfig.NewDefaultPitayaConfig()
	pitayaConfig.Heartbeat.Interval = cfg.Game.HeartbeatInterval
	pitayaConfig.Metrics.Prometheus.Enabled = true
	pitayaConfig.Metrics.Prometheus.Port = 9090

	builder := pitaya.NewDefaultBuilder(true, cfg.Server.Type, pitaya.Standalone, map[string]string{}, *pitayaConfig)
	builder.Serializer = serializer.NewSerializer()

	kcpAcc := network.NewKCPAcceptor(cfg)
	builder.AddAcceptor(kcpAcc)

	return builder
}

func NewPitayaApp(builder *pitaya.Builder) pitaya.Pitaya {
	return builder.Build()
}

func RegisterComponents(app pitaya.Pitaya, room *component.Room) {
	app.Register(room,
		pitayacomponent.WithName("room"),
		pitayacomponent.WithNameFunc(strings.ToLower),
	)
}

func StartApp(lc fx.Lifecycle, app pitaya.Pitaya, cfg *config.Config, _ logrus.FieldLogger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
			logger.Log.Infof("Pitaya server starting on %s (KCP Mode)", addr)
			go app.Start()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			app.Shutdown()
			return nil
		},
	})
}

func StartDashboard(lc fx.Lifecycle, room *component.Room, cfg *config.Config) {
	if !cfg.Dashboard.Enabled {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go dashboard.Start(room)
			return nil
		},
	})
}
