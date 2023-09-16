package main

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/redis/go-redis/v9"
)

// type jsonStreamPartition struct {
// 	Stream    *string `json:"stream"`
// 	Partition int32   `json:"partition"`
// 	Offset    int64   `json:"offset"`
// 	Error     *error  `json:"error"`
// }

type jsonMessage struct {
	Key    string                 `json:"key"`
	Values map[string]interface{} `json:"values"`
}

var rexJSONVal = regexp.MustCompile(`}$`)

// JSONBytesMake converts redis XMessage into JSON byte slice
func JSONBytesMake(msg *redis.XMessage, messageType string) ([]byte, error) {
	var jsonMessage = jsonMessage{
		Key:    msg.ID,
		Values: msg.Values,
	}

	b, err := json.Marshal(jsonMessage)
	var val string
	if messageType == "json" {
		val = ",\"value\":\"" + msg.ID + "\"}"
		// } else if messageType == "binary" {
		// 	val = ",\"value\":\"" + base64.StdEncoding.EncodeToString(msg.ID) + "\"}"
	} else {
		if jsonVal, err := json.Marshal(msg.ID); err == nil {
			val = ",\"value\":" + string(jsonVal) + "}"
		} else {
			err = fmt.Errorf("Can't stringify value as string: %v", err)
		}
	}

	return rexJSONVal.ReplaceAll(b, []byte(val)), err
}
