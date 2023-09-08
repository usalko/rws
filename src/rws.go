package main

import (
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
	RedisClientConfig        redis.ConfigMap
	RedisDefaultStreamConfig redis.ConfigMap
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

func copyConfigMap(m redis.ConfigMap) redis.ConfigMap {
	nm := make(redis.ConfigMap)
	for k, v := range m {
		nm[k] = v
	}
	return nm
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
	} else if kcfg, exists := rws.WebSockets[r.URL.Path]; exists {

		upgrader := ws.HTTPUpgrader{}
		if kcfg.Compression {
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
		config := copyConfigMap(kcfg.RedisClientConfig)
		// config["debug"] = "protocol"
		// config["broker.version.fallback"] = "0.8.0"
		config["session.timeout.ms"] = 6000
		config["go.events.channel.enable"] = true
		config["go.application.rebalance.enable"] = true
		if config["group.id"] == nil {
			config["group.id"] = parseQueryString(query, "group.id", "")
		}
		defStream := copyConfigMap(kcfg.RedisDefaultStreamConfig)
		if defStream["auto.offset.reset"] == nil {
			defStream["auto.offset.reset"] = parseQueryString(query, "auto.offset.reset", "")
		}
		config["default.stream.config"] = defStream
		streams := kcfg.RedisStreams
		if len(streams) == 0 {
			if t := parseQueryString(query, "streams", ""); t != "" {
				streams = strings.Split(t, ",")
			} else {
				log.Printf("No stream(s)")
				return
			}
		}

		// Instantiate client
		client, err := redis.NewClient(&config)
		if err != nil {
			fmt.Printf("Can't create client: %v\n", err)
			return
		}
		defer client.Close()

		// Make sure all streams actually exist
		meta, err := client.GetMetadata(nil, true, 5000)
		if err != nil {
			fmt.Printf("Can't get metadata: %v\n", err)
			return
		}
		for _, stream := range streams {
			if _, exists := meta.Streams[stream]; !exists {
				log.Printf("Stream [%s] doesn't exist", stream)
				return
			}
		}

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
		err = client.SubscribeStreams(streams, nil)
		if err != nil {
			fmt.Printf("Can't subscribe client: %v\n", err)
			return
		}
		defer client.Unsubscribe()

		log.Printf("Websocket opened %s\n", r.Host)
		// websocketMessageType := ws.TextMessage
		// if kcfg.MessageType == "binary" {
		// 	websocketMessageType = ws.BinaryMessage
		// }
		running := true
		// Keep reading and sending messages
		for running {
			select {
			// Exit if websocket read fails
			case <-chClose:
				running = false
			case ev := <-client.Events():
				switch e := ev.(type) {
				case redis.AssignedPartitions:
					client.Assign(e.Partitions)
				case redis.RevokedPartitions:
					client.Unassign()
				case *redis.Message:
					if kcfg.MessageDetails {
						val, err2 := JSONBytefy(e, kcfg.MessageType)
						if err2 == nil {
							err = wsutil.WriteServerMessage(wscon, ws.OpBinary, val)
						} else {
							err = wsutil.WriteServerMessage(wscon, ws.OpBinary, e.Value)
						}

					} else {
						err = wsutil.WriteServerMessage(wscon, ws.OpBinary, e.Value)
					}
					if err != nil {
						// handle error
						chClose <- true
						log.Printf("WebSocket write error: %v (%v)\n", err, e)
						running = false
					}
				case redis.PartitionEOF:
					// log.Printf("%% Reached %v\n", e)
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
