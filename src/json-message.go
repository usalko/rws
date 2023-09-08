package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
)

type jsonStreamPartition struct {
	Stream    *string `json:"stream"`
	Partition int32   `json:"partition"`
	Offset    int64   `json:"offset"`
	Error     *error  `json:"error"`
}

type jsonMessage struct {
	Key             string              `json:"key"`
	Headers         []jsonHeader        `json:"headers"`
	StreamPartition jsonStreamPartition `json:"partition"`
	Timestamp       time.Time           `json:"timestamp"`
	TimestampType   redis.TimestampType `json:"timestampType"`
}

type jsonHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

var rexJSONVal = regexp.MustCompile(`}$`)

// JSONBytefy coverts redis Message into JSON byte slice
func JSONBytefy(msg *redis.Message, messageType string) ([]byte, error) {
	var jsonMsg = jsonMessage{
		Key:           string(msg.Key),
		Timestamp:     msg.Timestamp,
		TimestampType: msg.TimestampType,
	}
	jsonMsg.StreamPartition = jsonStreamPartition{
		Stream:    msg.StreamPartition.Stream,
		Partition: msg.StreamPartition.Partition,
		Offset:    int64(msg.StreamPartition.Offset),
		Error:     &msg.StreamPartition.Error,
	}

	for _, header := range msg.Headers {
		jsonMsg.Headers = append(jsonMsg.Headers, jsonHeader{Key: header.Key, Value: string(header.Value)})
	}

	b, err := json.Marshal(jsonMsg)
	var val string
	if messageType == "json" {
		val = ",\"value\":" + string(msg.Value) + "}"
	} else if messageType == "binary" {
		val = ",\"value\":\"" + base64.StdEncoding.EncodeToString(msg.Value) + "\"}"
	} else {
		if jsonVal, err := json.Marshal(string(msg.Value)); err == nil {
			val = ",\"value\":" + string(jsonVal) + "}"
		} else {
			err = fmt.Errorf("Can't stringify value as string: %v", err)
		}
	}

	return rexJSONVal.ReplaceAll(b, []byte(val)), err
}
