package main

import (
	"context"
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
	WebSockets  map[string]*RWSRedis
	TestUIs     map[string]*string
}

// RWSRedis Redis config
type RWSRedis struct {
	RedisClientConfig        redis.Options
	RedisDefaultStreamConfig redis.Options
	RedisStreams             []string
	MessageDetails           bool
	MessageType              string
	Compression              bool
}

type templateInfo struct {
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

func parseQueryString(query url.Values, key string, val string) string {
	if val == "" {
		if vals, exists := query[key]; exists && len(vals) > 0 {
			return vals[0]
		}
	}
	return val
}

// Start start websocket and start consuming from Redis stream(s)
func (rws *RWS) Start() error {
	if rws.TLSCertFile != "" {
		return http.ListenAndServeTLS(rws.Address, rws.TLSCertFile, rws.TLSKeyFile, rws)
	}
	return http.ListenAndServe(rws.Address, rws)
}

func copyConfigMap(options redis.Options) redis.Options {
	nm := options
	return nm
}

// Event generic interface
type Event interface {
	// String returns a human-readable representation of the event
	// String() string
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
			testTemplate.Execute(w, templateInfo{strings.TrimRight(r.URL.Path, "/"), wsURL})
		}
		return
	} else if rcfg, exists := rws.WebSockets[r.URL.Path]; exists {

		upgrader := ws.HTTPUpgrader{}
		if rcfg.Compression {
			e := wsflate.Extension{
				// We are using default parameters here since we use
				// wsflate.{Compress,Decompress}Frame helpers below in the code.
				// This assumes that we use standard compress/flate package as flate
				// implementation.
				Parameters: wsflate.DefaultParameters,
			}
			upgrader = ws.HTTPUpgrader{
				Negotiate: e.Negotiate,
			}
		}
		wscon, _, _, err := upgrader.Upgrade(r, w)

		if err != nil {
			log.Printf("Websocket http upgrade failed: %v\n", err)
			return
		}

		// Read redis params from query string
		query := r.URL.Query()
		config := copyConfigMap(rcfg.RedisClientConfig)
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

		streams := rcfg.RedisStreams
		if len(streams) == 0 {
			if t := parseQueryString(query, "streams", ""); t != "" {
				streams = strings.Split(t, ",")
			} else {
				log.Printf("No stream(s)")
				return
			}
		}

		// Instantiate client
		client := redis.NewClient(&config)
		defer client.Close()

		// Context
		ctx := context.Background()

		// Make sure to read client message and react on close/error
		chClose := make(chan bool)

		go func() {
			defer wscon.Close()

			for {
				_, _, err := wsutil.ReadClientData(wscon)
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

		// Subscribe client to the streams
		chEvent := make(chan Event)

		go func() {
			defer client.Close()

			for {
				xStreams, err := client.XReadStreams(ctx, streams...).Result()
				if err != nil {
					fmt.Printf("Can't read streams: %v\n", err)
					chEvent <- err
					return
				}
				for _, xStream := range xStreams {
					chEvent <- xStream.Messages
				}
			}
		}()

		log.Printf("Websocket opened %s\n", r.Host)
		// websocketMessageType := ws.TextMessage
		// if rcfg.MessageType == "binary" {
		// 	websocketMessageType = ws.BinaryMessage
		// }
		running := true
		// Keep reading and sending messages
		for running {
			select {
			// Exit if websocket read fails
			case <-chClose:
				running = false
			case ev := <-chEvent:
				switch e := ev.(type) {
				case *redis.XMessage:
					if rcfg.MessageDetails {
						val, err2 := JSONBytesMake(e, rcfg.MessageType)
						if err2 == nil {
							err = wsutil.WriteServerMessage(wscon, ws.OpBinary, val)
						} else {
							err = wsutil.WriteServerMessage(wscon, ws.OpBinary, []byte("e.Values"))
						}

					} else {
						err = wsutil.WriteServerMessage(wscon, ws.OpBinary, []byte("e.Values"))
					}
					if err != nil {
						// handle error
						chClose <- true
						log.Printf("WebSocket write error: %v (%v)\n", err, e)
						running = false
					}
				case redis.Error:
					log.Printf("%% Error: %v\n", e)
					err = wscon.Close()
					if err != nil {
						log.Printf("Error while closing WebSocket: %v\n", e)
					}
					running = false
				}
			}
		}
		log.Printf("Websocket closed %s\n", r.Host)
		return
	}
	w.WriteHeader(404)
}
