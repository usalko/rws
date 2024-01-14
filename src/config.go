package main

import (
	"fmt"
	"io/ioutil"
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
      # https://github.com/edenhill/librdredis/blob/master/CONFIGURATION.md#global-configuration-properties
      metadata.broker.list: localhost:6379
      group.id: my-redis-group
    redis.default.stream.config:
      auto.offset.reset: latest
    redis.streams:
      - my.redis.stream
    address: :9999
    # message.details: false
    # message.type: json
    # endpoint.prefix: ""
    # endpoint.websocket: ws
    # endpoint.test: test
    # on.close.key: my.redis.stream.is_closed
    # on.close.value: true
`

// ConfigRWS Redis to websocket YAML
type ConfigRWS struct {
	RedisClientConfig        map[string]interface{} `yaml:"redis.client.config"`
	RedisDefaultStreamConfig map[string]interface{} `yaml:"redis.default.stream.config"`
	RedisStreams             []string               `yaml:"redis.streams"`
	Address                  string                 `yaml:"address"`
	EndpointPrefix           string                 `yaml:"endpoint.prefix"`
	EndpointTest             string                 `yaml:"endpoint.test"`
	EndpointWS               string                 `yaml:"endpoint.websocket"`
	OnCloseKey               string                 `yaml:"on.close.key"`
	OnCloseValue             string                 `yaml:"on.close.value"`
	MessageType              string                 `yaml:"message.type"`
	Compression              bool                   `yaml:"compression"`
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
	for _, rwsConfig := range config.ConfigRWSs {
		var rws *RWS
		var exists bool
		if rws, exists = rwsMap[rwsConfig.Address]; !exists {
			rws = &RWS{
				Address:     rwsConfig.Address,
				TLSCertFile: certFile,
				TLSKeyFile:  keyFile,
				SourceFile:  filename,
				WebSockets:  make(map[string]*RWSRedis),
				TestUIs:     make(map[string]*string),
			}
			rwsMap[rwsConfig.Address] = rws
		}
		if rwsConfig.MessageType == "" {
			rwsConfig.MessageType = "json"
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
		// if rwsConfig.RedisClientConfig["group.id"] == "" {
		// 	panic(fmt.Sprintf("group.id must be defined, address [%s]", rwsConfig.Address))
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
		if rwsConfig.MessageType != "json" &&
			rwsConfig.MessageType != "text" &&
			rwsConfig.MessageType != "binary" {
			panic(fmt.Sprintf("invalid message.type [%s]", rwsConfig.MessageType))
		}
		rws.TestUIs[testPath] = &wsPath
		rws.WebSockets[wsPath] = &RWSRedis{
			RedisClientConfig:        rwsConfig.RedisClientConfig,
			RedisDefaultStreamConfig: rwsConfig.RedisDefaultStreamConfig,
			RedisStreams:             rwsConfig.RedisStreams,
			MessageType:              rwsConfig.MessageType,
			Compression:              rwsConfig.Compression,
			OnCloseKey:               rwsConfig.OnCloseKey,
			OnCloseValue:             rwsConfig.OnCloseValue,
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
