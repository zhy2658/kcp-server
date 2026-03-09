package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server struct {
		Host  string
		Port  int
		Type  string
		Debug bool
	}
	KCP struct {
		NoDelay    int
		Interval   int
		Resend     int
		NC         int
		SndWnd     int
		RcvWnd     int
		AckNoDelay bool
	}
	Log struct {
		Level      string
		Filename   string
		MaxSize    int
		MaxBackups int
		MaxAge     int
	}
	Game struct {
		HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`
		MaxRoomPlayers    int           `mapstructure:"max_room_players"`
	}
}

var Conf *Config

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	conf := &Config{}
	if err := viper.Unmarshal(conf); err != nil {
		return nil, err
	}

	// Keep global for backward compatibility if needed, but return instance for DI
	Conf = conf
	return conf, nil
}
