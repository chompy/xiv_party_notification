package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
)

const messageUrl = "https://api.pushover.net/1/messages.json"
const configPath = "config.yml"

var spaceCapitalRegex = regexp.MustCompile("([a-z'])([A-Z])")
var config = Config{}

type Config struct {
	WebsocketPort    int    `yaml:"websocket_port"`
	PushoverAppToken string `yaml:"pushover_app_token"`
	PushoverUserKey  string `yaml:"pushover_user_key"`
	NotifyOnFill     bool   `yaml:"notifiy_on_fill"`
	NotifyOnDisband  bool   `yaml:"notifiy_on_disband"`
	NotifyOnJoin     bool   `yaml:"notify_on_join"`
	NotifyOnLeave    bool   `yaml:"notify_on_leave"`
}

type Message struct {
	Type string      `json:"msgtype"`
	Data interface{} `json:"msg"`
}

type LogLine struct {
	Time time.Time
	Code int64
	Name string
	Line string
}

type Notification struct {
	Title   string
	Message string
	Sound   string
}

func loadConfig() error {
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(rawConfig, &config)
}

func addSpaceAfterCapitals(input string) string {
	return spaceCapitalRegex.ReplaceAllString(input, "$1 $2")
}

func decodeMessage(message []byte) (Message, error) {
	out := Message{}
	return out, json.Unmarshal(message, &out)
}

func readLogLing(data interface{}) LogLine {
	splitString := strings.Split(data.(string), "|")
	if splitString[0] != "00" {
		return LogLine{}
	}

	timestamp, err := time.Parse(time.RFC3339Nano, splitString[1])
	if err != nil {
		log.Println("Unable to parse log timestamp: ", err)
		return LogLine{}
	}

	code := new(big.Int)
	code.SetString(splitString[2], 16)
	return LogLine{
		Time: timestamp,
		Code: code.Int64(),
		Name: splitString[3],
		Line: splitString[4],
	}
}

func buildNotification(logLine LogLine) *Notification {
	switch logLine.Code {
	case 57: // party filled/disbanded
		{
			if config.NotifyOnFill && strings.Contains(logLine.Line, "have been filled") {
				return &Notification{
					Title:   "Your Party Has Filled",
					Message: logLine.Line,
					Sound:   "gamelan",
				}
			} else if config.NotifyOnDisband && strings.Contains(logLine.Line, "has been disbanded") {
				return &Notification{
					Title:   "Your Party Has Disbanded",
					Message: logLine.Line,
					Sound:   "none",
				}
			}
		}
	case 8761: // join/leave/return to party
		{
			if config.NotifyOnJoin && strings.Contains(logLine.Line, "joins the party") {
				return &Notification{
					Title:   "Player Joined Your Party",
					Message: addSpaceAfterCapitals(logLine.Line),
					Sound:   "none",
				}
			} else if config.NotifyOnLeave && strings.Contains(logLine.Line, "left the party") {
				return &Notification{
					Title:   "Player Left Your Party",
					Message: addSpaceAfterCapitals(logLine.Line),
					Sound:   "none",
				}
			}
			break
		}
	}

	return nil
}

func sendNotification(notification *Notification) {
	data := map[string]string{
		"token":   config.PushoverAppToken,
		"user":    config.PushoverUserKey,
		"title":   notification.Title,
		"message": notification.Message,
		"sound":   notification.Sound,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Println("Unable to encode notification: ", err)
		return
	}
	if _, err := http.Post(messageUrl, "application/json", bytes.NewReader(jsonData)); err != nil {
		log.Println("Unable to send notification: ", err)
		return
	}
	log.Printf("Sent notification: %s", notification.Title)
}

func main() {

	if err := loadConfig(); err != nil {
		log.Fatal("Unable to read config: ", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", config.WebsocketPort), Path: "MiniParse"}

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("Unable to connect to websocket server:", err)
	}
	defer c.Close()

	log.Printf("Connected to websocket server at %s.", u.String())

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, rawMessage, err := c.ReadMessage()
			if err != nil {
				log.Println("Unable to fetch message:", err)
				return
			}
			message, err := decodeMessage(rawMessage)
			if err != nil {
				log.Println("Unable to decode message: ", err)
				return
			}
			if message.Type == "Chat" {
				logLing := readLogLing(message.Data)
				notification := buildNotification(logLing)
				if notification != nil {
					sendNotification(notification)
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("Interupt detected. Closing connection.")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}

}
