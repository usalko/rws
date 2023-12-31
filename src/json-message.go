package main

import (
	"encoding/json"
	"fmt"
	"strings"

	// "regexp"

	"github.com/redis/go-redis/v9"
)

// type jsonStreamPartition struct {
// 	Stream    *string `json:"stream"`
// 	Partition int32   `json:"partition"`
// 	Offset    int64   `json:"offset"`
// 	Error     *error  `json:"error"`
// }

// type jsonMessage struct {
// 	Key    string                 `json:"key"`
// 	Values map[string]interface{} `json:"values"`
// }

// var rexJSONVal = regexp.MustCompile(`}$`)

// JSONBytesMake converts redis XMessage into JSON byte slice
func JSONBytesMake(messages []redis.XMessage, messageType string) ([]byte, error) {

	if messageType == "json" {
		jsonMessages := messages
		for i, message := range messages {
			unescapedValues := make(map[string]interface{})
			for key, value := range message.Values {
				textValue := fmt.Sprintf("%v", value)
				// Unpack json object (cause redis doesn't have complex field types)
				if strings.HasPrefix(textValue, "{") || strings.HasPrefix(textValue, "[") {
					var objectValue map[string]interface{}
					err := json.Unmarshal([]byte(textValue), &objectValue)
					if err != nil {
						return nil, err
					}
					unescapedValues[key] = objectValue
				} else {
					unescapedValues[key] = value
				}
			}
			jsonMessages[i] = redis.XMessage{
				ID:     message.ID,
				Values: unescapedValues,
			}
		}

		bytes, err := json.Marshal(jsonMessages)

		return bytes, err
	}

	bytes, err := json.Marshal(messages)

	return bytes, err
}
