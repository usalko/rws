package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

const initConfig = `schema.version: "1.0"
# tls.cert.file: my-domain.crt
# tls.key.file: my-domain.key
redis.to.websocket:
  - redis.client.config:
      metadata.broker.list: localhost:6379
    redis.default.stream.config:
      auto.offset.reset: latest
    #
    # Input redis streams
    redis.streams.to.ws:
      - my.redis.stream.for.input
    redis.streams.to.ws.message.format: json
	#
	# Output redis streams
    ws.to.redis.streams:
      - my.redis.stream.for.output
	ws.to.redis.streams.message.format: json
	#
	# Binding address
    address: :9999
    # endpoint.prefix: ""
    # endpoint.websocket: ws
    # endpoint.test: test
	# compression: false
`

// ConfigRws Redis to websocket YAML
type ConfigRws struct {
	RedisClientConfig             map[string]interface{} `yaml:"redis.client.config"`
	RedisDefaultStreamConfig      map[string]interface{} `yaml:"redis.default.stream.config"`
	RedisStreamsToWs              []string               `yaml:"redis.streams.to.ws"`
	WsToRedisStreams              []string               `yaml:"ws.to.redis.streams"`
	Address                       string                 `yaml:"address"`
	EndpointPrefix                string                 `yaml:"endpoint.prefix"`
	EndpointTest                  string                 `yaml:"endpoint.test"`
	EndpointWS                    string                 `yaml:"endpoint.websocket"`
	RedisStreamsToWsMessageFormat string                 `yaml:"redis.streams.to.ws.message.format"`
	WsToRedisStreamsMessageFormat string                 `yaml:"ws.to.redis.streams.message.format"`
	Compression                   bool                   `yaml:"compression"`
}

// Config YAML config file
type Config struct {
	SchemaVersion string      `yaml:"schema.version"`
	TLSCertFile   string      `yaml:"tls.cert.file"`
	TLSKeyFile    string      `yaml:"tls.key.file"`
	ConfigsRws    []ConfigRws `yaml:"redis.to.websocket"`
}

// ReadRws read config file and returns collection of RWS
func ReadRws(filename string) []*RWS {
	fileContent, err := os.ReadFile(filename)
	if err != nil {
		absPath, _ := filepath.Abs(filename)
		log.Fatalf("Error while reading %v file: \n%v ", absPath, err)
	}
	log.Printf("%s\n%s", filename, string(fileContent))
	var config Config
	err = yaml.Unmarshal(fileContent, &config)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}

	certFile := ""
	keyFile := ""
	if config.TLSCertFile != "" && config.TLSKeyFile == "" || config.TLSCertFile == "" && config.TLSKeyFile != "" {
		panic(fmt.Sprintf("Both certificate and key file must be defined %v", "."))
	} else if config.TLSCertFile != "" {
		if _, err := os.Stat(config.TLSCertFile); err == nil {
			if _, err := os.Stat(config.TLSKeyFile); err == nil {
				keyFile = config.TLSKeyFile
				certFile = config.TLSCertFile
			} else {
				panic(fmt.Sprintf("key file %s does not exist", config.TLSKeyFile))
			}
		} else {
			panic(fmt.Sprintf("certificate file %s does not exist", config.TLSKeyFile))
		}
	}
	rwsMap := make(map[string]*RWS)
	for _, rwsConfig := range config.ConfigsRws {
		var rws *RWS
		var exists bool
		if rws, exists = rwsMap[rwsConfig.Address]; !exists {
			rws = &RWS{
				Address:     rwsConfig.Address,
				TLSCertFile: certFile,
				TLSKeyFile:  keyFile,
				SourceFile:  filename,
				WebSockets:  make(map[string]*RwsRedis),
				TestUIs:     make(map[string]*string),
			}
			rwsMap[rwsConfig.Address] = rws
		}
		if rwsConfig.RedisStreamsToWsMessageFormat == "" {
			rwsConfig.RedisStreamsToWsMessageFormat = "json"
		}
		if rwsConfig.WsToRedisStreamsMessageFormat == "" {
			rwsConfig.WsToRedisStreamsMessageFormat = "json"
		}
		testPath := rwsConfig.EndpointTest
		wsPath := rwsConfig.EndpointWS
		if testPath == "" && wsPath == "" {
			testPath = "test"
		}
		if rwsConfig.EndpointPrefix != "" {
			testPath = rwsConfig.EndpointPrefix + "/" + testPath
			wsPath = rwsConfig.EndpointPrefix + "/" + wsPath
		}
		testPath = "/" + strings.TrimRight(testPath, "/")
		wsPath = "/" + strings.TrimRight(wsPath, "/")

		if testPath == wsPath {
			panic(fmt.Sprintf("test path and websocket path can't be same [%s]", rwsConfig.EndpointTest))
		}
		if rwsConfig.RedisClientConfig["metadata.broker.list"] == "" {
			panic(fmt.Sprintf("metadata.broker.list must be defined, address [%s]", rwsConfig.Address))
		}
		if _, exists := rws.TestUIs[testPath]; exists {
			panic(fmt.Sprintf("test path [%s] already defined", testPath))
		}
		if _, exists := rws.WebSockets[testPath]; exists {
			panic(fmt.Sprintf("test path [%s] already defined as websocket path", testPath))
		}
		if _, exists := rws.WebSockets[wsPath]; exists {
			panic(fmt.Sprintf("websocket path [%s] already defined", wsPath))
		}
		if _, exists := rws.TestUIs[wsPath]; exists {
			panic(fmt.Sprintf("websocket path [%s] already defined as test path", wsPath))
		}
		if rwsConfig.RedisStreamsToWsMessageFormat != "json" &&
			rwsConfig.RedisStreamsToWsMessageFormat != "csv" &&
			rwsConfig.RedisStreamsToWsMessageFormat != "simple-json" {
			panic(fmt.Sprintf(
				"invalid redis.streams.to.ws.message.format [%s], it's should be one of the values: json, csv, simple-json",
				rwsConfig.RedisStreamsToWsMessageFormat,
			))
		}
		if rwsConfig.WsToRedisStreamsMessageFormat != "json" &&
			rwsConfig.WsToRedisStreamsMessageFormat != "csv" &&
			rwsConfig.WsToRedisStreamsMessageFormat != "simple-json" {
			panic(fmt.Sprintf(
				"invalid ws.to.redis.streams.message.format [%s], it's should be one of the values: json, csv, simple-json",
				rwsConfig.WsToRedisStreamsMessageFormat,
			))
		}
		rws.TestUIs[testPath] = &wsPath
		rws.WebSockets[wsPath] = &RwsRedis{
			RedisClientConfig:             rwsConfig.RedisClientConfig,
			RedisDefaultStreamConfig:      rwsConfig.RedisDefaultStreamConfig,
			RedisStreamsToWs:              rwsConfig.RedisStreamsToWs,
			RedisStreamsToWsMessageFormat: rwsConfig.RedisStreamsToWsMessageFormat,
			WsToRedisStreams:              rwsConfig.WsToRedisStreams,
			WsToRedisStreamsMessageFormat: rwsConfig.WsToRedisStreamsMessageFormat,
			Compression:                   rwsConfig.Compression,
		}
	}
	rwsSlice := make([]*RWS, len(rwsMap))
	i := 0
	for _, rws := range rwsMap {
		rwsSlice[i] = rws
		i++
	}
	return rwsSlice
}
