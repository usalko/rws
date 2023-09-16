package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/redis/go-redis/v9"

	yaml "gopkg.in/yaml.v2"
)

const initConfig = `schema.version: "1.0"
# tls.cert.file: my-domain.crt
# tls.key.file: my-domain.key
redis.to.websocket:
  - redis.client.config:
      # https://github.com/edenhill/librdredis/blob/master/CONFIGURATION.md#global-configuration-properties
      metadata.broker.list: localhost:9092
      enable.auto.commit: false
      group.id: my-redis-group
    redis.default.stream.config:
      # https://github.com/edenhill/librdredis/blob/master/CONFIGURATION.md#stream-configuration-properties
      auto.offset.reset: latest
    redis.streams:
      - my.redis.stream
    address: :9999
    # message.details: false
    # message.type: json
    # endpoint.prefix: ""
    # endpoint.websocket: ws
    # endpoint.test: test
`

// ConfigRWS Redis to websocket YAML
type ConfigRWS struct {
	RedisClientConfig        redis.Options `yaml:"redis.client.config"`
	RedisDefaultStreamConfig redis.Options `yaml:"redis.default.stream.config"`
	RedisStreams             []string      `yaml:"redis.streams"`
	Address                  string        `yaml:"address"`
	EndpointPrefix           string        `yaml:"endpoint.prefix"`
	EndpointTest             string        `yaml:"endpoint.test"`
	EndpointWS               string        `yaml:"endpoint.websocket"`
	MessageDetails           bool          `yaml:"message.details"`
	MessageType              string        `yaml:"message.type"`
	Compression              bool          `yaml:"compression"`
}

// Config YAML config file
type Config struct {
	SchemaVersion string      `yaml:"schema.version"`
	TLSCertFile   string      `yaml:"tls.cert.file"`
	TLSKeyFile    string      `yaml:"tls.key.file"`
	ConfigRWSs    []ConfigRWS `yaml:"redis.to.websocket"`
}

// ReadRWS read config file and returns collection of RWS
func ReadRWS(filename string) []*RWS {
	fileContent, err := ioutil.ReadFile(filename)
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
		panic(fmt.Sprintf("Both certificate and key file must be defined"))
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
	for _, rwsc := range config.ConfigRWSs {
		var rws *RWS
		var exists bool
		if rws, exists = rwsMap[rwsc.Address]; !exists {
			rws = &RWS{
				Address:     rwsc.Address,
				TLSCertFile: certFile,
				TLSKeyFile:  keyFile,
				WebSockets:  make(map[string]*RWSRedis),
				TestUIs:     make(map[string]*string),
			}
			rwsMap[rwsc.Address] = rws
		}
		if rwsc.MessageType == "" {
			rwsc.MessageType = "json"
		}
		testPath := rwsc.EndpointTest
		wsPath := rwsc.EndpointWS
		if testPath == "" && wsPath == "" {
			testPath = "test"
		}
		if rwsc.EndpointPrefix != "" {
			testPath = rwsc.EndpointPrefix + "/" + testPath
			wsPath = rwsc.EndpointPrefix + "/" + wsPath
		}
		testPath = "/" + strings.TrimRight(testPath, "/")
		wsPath = "/" + strings.TrimRight(wsPath, "/")

		if testPath == wsPath {
			panic(fmt.Sprintf("test path and websocket path can't be same [%s]", rwsc.EndpointTest))
		}
		if rwsc.RedisClientConfig.Addr == "" {
			panic(fmt.Sprintf("metadata.broker.list must be defined, address [%s]", rwsc.Address))
		}
		// if rwsc.RedisClientConfig["group.id"] == "" {
		// 	panic(fmt.Sprintf("group.id must be defined, address [%s]", rwsc.Address))
		// }
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
		if rwsc.MessageType != "json" &&
			rwsc.MessageType != "text" &&
			rwsc.MessageType != "binary" {
			panic(fmt.Sprintf("invalid message.type [%s]", rwsc.MessageType))
		}
		rws.TestUIs[testPath] = &wsPath
		rws.WebSockets[wsPath] = &RWSRedis{
			RedisClientConfig:        rwsc.RedisClientConfig,
			RedisDefaultStreamConfig: rwsc.RedisDefaultStreamConfig,
			RedisStreams:             rwsc.RedisStreams,
			MessageDetails:           rwsc.MessageDetails,
			MessageType:              rwsc.MessageType,
			Compression:              rwsc.Compression,
		}
	}
	rwss := make([]*RWS, len(rwsMap))
	i := 0
	for _, rws := range rwsMap {
		rwss[i] = rws
		i++
	}
	return rwss
}
