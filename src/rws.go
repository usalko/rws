package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsflate"
	"github.com/gobwas/ws/wsutil"
	"github.com/redis/go-redis/v9"
)

// RWS Redis to websocket config
type RWS struct {
	Address     string
	TLSCertFile string
	TLSKeyFile  string
	SourceFile  string
	WebSockets  map[string]*RWSRedis
	TestUIs     map[string]*string
}

// RWSRedis Redis config
type RWSRedis struct {
	RedisClientConfig        map[string]interface{}
	RedisDefaultStreamConfig map[string]interface{}
	RedisStreams             []string
	MessageType              string
	Compression              bool
}

type TemplateInfo struct {
	TestPath, WSURL string
}

var localStatic = false
var testTemplate *template.Template
var rexStatic = regexp.MustCompile(`(.*)(/static/.+(\.[a-z0-9]+))$`)
var mimeTypes = map[string]string{
	".js":   "application/javascript",
	".htm":  "text/html; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".json": "application/json",
	".xml":  "text/xml; charset=utf-8",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".gif":  "image/gif",
	".pdf":  "application/pdf",
}

// func parseQueryString(query url.Values, key string, val string) string {
// 	if val == "" {
// 		if values, exists := query[key]; exists && len(values) > 0 {
// 			return values[0]
// 		}
// 	}
// 	return val
// }

// Start start websocket and start consuming from Redis stream(s)
func (rws *RWS) Start() error {
	if rws.TLSCertFile != "" {
		return http.ListenAndServeTLS(rws.Address, rws.TLSCertFile, rws.TLSKeyFile, rws)
	}
	return http.ListenAndServe(rws.Address, rws)
}

// config["debug"] = "protocol"
// config["broker.version.fallback"] = "0.8.0"
// config["session.timeout.ms"] = 6000
// config["go.events.channel.enable"] = true
// config["go.application.rebalance.enable"] = true
// if config["group.id"] == nil {
// 	config["group.id"] = parseQueryString(query, "group.id", "")
// }
// defStream := copyConfigMap(rcfg.RedisDefaultStreamConfig)
// if defStream["auto.offset.reset"] == nil {
// 	defStream["auto.offset.reset"] = parseQueryString(query, "auto.offset.reset", "")
// }
// config["default.stream.config"] = defStream

func redisOptionsFromRwsConfiguration(redisClientConfig map[string]interface{}, query url.Values) redis.Options {
	return redis.Options{
		Addr: fmt.Sprint(redisClientConfig["metadata.broker.list"]),
	}
}

func redisStreams(query url.Values, defaultStreams []string) []string {
	streams := query.Get("topics")
	if streams != "" {
		return strings.Split(streams, ",")
	}
	return defaultStreams
}

func (rws *RWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	submatch := rexStatic.FindStringSubmatch(r.URL.Path)
	if len(submatch) > 0 {
		// serve static files
		testUIPath := submatch[1]
		if testUIPath == "" {
			testUIPath = "/"
		}
		if _, exists := rws.TestUIs[testUIPath]; exists {
			if payload, err := FSByte(localStatic, submatch[2]); err == nil {
				var mime = "application/octet-stream"
				if m, ok := mimeTypes[submatch[3]]; ok {
					mime = m
				}
				w.Header().Add("Content-Type", mime)
				w.Header().Add("Content-Length", fmt.Sprintf("%d", len(payload)))
				w.Write(payload)
				return
			}
		}
	} else if wsPath, exists := rws.TestUIs[r.URL.Path]; exists {
		html, err := FSString(localStatic, "/static/test.html")
		if err == nil {
			wsURL := "ws://" + r.Host + *wsPath
			if rws.TLSCertFile != "" {
				wsURL = "wss://" + r.Host + *wsPath
			}
			if testTemplate == nil || localStatic {
				testTemplate = template.Must(template.New("").Parse(html))
			}
			testTemplate.Execute(w, TemplateInfo{strings.TrimRight(r.URL.Path, "/"), wsURL})
		}
		return
	} else if rwsConfig, exists := rws.WebSockets[r.URL.Path]; exists {

		upGrader := ws.HTTPUpgrader{}
		if rwsConfig.Compression {
			e := wsflate.Extension{
				// We are using default parameters here since we use
				// wsflate.{Compress,Decompress}Frame helpers below in the code.
				// This assumes that we use standard compress/flate package as flate
				// implementation.
				Parameters: wsflate.DefaultParameters,
			}
			upGrader = ws.HTTPUpgrader{
				Negotiate: e.Negotiate,
			}
		}
		wsConnection, _, _, err := upGrader.Upgrade(r, w)

		if err != nil {
			log.Printf("Websocket http upgrade failed: %v\n", err)
			return
		}

		// Read redis params from query string
		query := r.URL.Query()
		options := redisOptionsFromRwsConfiguration(rwsConfig.RedisClientConfig, query)

		streams := redisStreams(query, rwsConfig.RedisStreams)
		if len(streams) == 0 {
			log.Printf("No stream(s), please setup 'default.stream.config' section in configuration (%s) or pass topic(s) as query parameter.", rws.SourceFile)
			return
		}

		// Instantiate client
		client := redis.NewClient(&options)
		defer client.Close()

		// Context
		ctx := context.Background()

		// Make sure to read client message and react on close/error
		chClose := make(chan bool)

		go func() {
			defer wsConnection.Close()

			for {
				_, _, err := wsutil.ReadClientData(wsConnection)
				if err != nil {
					// handle error
					chClose <- true
					if !strings.HasPrefix(err.Error(), "websocket: close") {
						log.Printf("WebSocket read error: %v\n", err)
					}
					return
				}
			}
		}()

		// Subscribe client to the stream messages
		chStream := make(chan redis.XStream)

		// Subscribe client to the errors
		chError := make(chan error)

		go func() {
			defer client.Close()

			if len(streams) == 0 {
				chError <- errors.New("no streams for listening")
				return
			}

			// Filter for existing streams
			streamsRequest := make([]string, 0)
			for _, stream := range streams {
				existedStreams, _, err := client.Scan(ctx, 0, stream, -1).Result()
				if err != nil {
					fmt.Printf("Can't read streams %v: %v\n", streams, err)
					chError <- err
					return
				}
				fmt.Printf("Success scan the stream: %v\nExisted streams: %v\n", stream, existedStreams)
				streamsRequest = append(streamsRequest, existedStreams...)
			}

			if len(streamsRequest) == 0 {
				errorDescription := fmt.Sprintf("The streams: %v not found in redis\n", streams)
				chError <- errors.New(errorDescription)
				return
			}

			idsOffset := len(streamsRequest)
			ids := make([]string, idsOffset)
			for i := 0; i < len(streamsRequest); i++ {
				ids[i] = "0" // '$' argument see redis documentation
			}
			streamsRequest = append(streamsRequest, ids...)
			for {
				xStreams, err := client.XRead(ctx, &redis.XReadArgs{
					Streams: streamsRequest,
					Block:   0,
				}).Result()
				if err != nil {
					fmt.Printf("Can't read streams %v: %v\n", streams, err)
					chError <- err
					return
				}
				for _, xStream := range xStreams {
					chStream <- xStream
				}
			}
		}()

		log.Printf("Websocket opened %s\n", r.Host)
		running := true
		// Keep reading and sending messages
		for running {
			select {
			// Exit if websocket read fails
			case <-chClose:
				running = false
			case ev := <-chError:
				switch e := ev.(type) {
				case redis.Error:
					if e.Error() == "redis: nil" {
						log.Printf("%% Error: %v perhaps stream(s) %v didn't exists\n", e, streams)
					} else {
						log.Printf("%% Error: %v\n", e)
					}
					err = wsConnection.Close()
					if err != nil {
						log.Printf("Error while closing WebSocket: %v\n", e)
					}
					running = false
				default:
					log.Printf("Error type: %v", ev)
					err = wsConnection.Close()
					if err != nil {
						log.Printf("Error while closing WebSocket: %v\n", e)
					}
					running = false
				}
			case stream := <-chStream:
				values, jsonErrors := JSONBytesMake(stream.Messages, rwsConfig.MessageType)
				if jsonErrors == nil {
					err = wsutil.WriteServerMessage(wsConnection, ws.OpBinary, values)
				} else {
					err = wsutil.WriteServerMessage(wsConnection, ws.OpBinary, []byte(jsonErrors.Error()))
				}

				if err != nil {
					// handle error
					chClose <- true
					log.Printf("WebSocket write error: %v (%v)\n", err, stream)
					running = false
				} else {
					for _, xMessage := range stream.Messages {
						// TODO: call single command with multiply message ids
						client.XDel(ctx, stream.Stream, xMessage.ID)
					}
				}
			}
		}
		log.Printf("Websocket closed %s\n", r.Host)
		return
	}
	w.WriteHeader(404)
}
