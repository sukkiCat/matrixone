package config

import (
	cConfig "github.com/matrixorigin/matrixcube/config"
	"github.com/matrixorigin/matrixcube/server"
)

type Config struct {
	CubeConfig cConfig.Config
	ServerConfig server.Cfg
	ClusterConfig ClusterConfig
}

type ClusterConfig struct {
	PreAllocatedGroupNum uint64
	MaxGroupNum uint64
}