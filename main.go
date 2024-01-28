package main

import (
	"bytes"
	"encoding/json"
	"flag"
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
)

const messageUrl = "https://api.pushover.net/1/messages.json"
const appToken = "<YOUR APP TOKEN HERE>"

var spaceCapitalRegex = regexp.MustCompile("([a-z'])([A-Z])")

var serverAddress = flag.String("addr", "127.0.0.1:10501", "ACT/INNACT websocket server address")
var pushoverUserKey = flag.String("key", "", "Pushover user key")
var notifyOnJoin = flag.Bool("notify-join", false, "Send a notification when someone joins the party")
var notifyOnLeave = flag.Bool("notify-leave", false, "Send a notification when someone leaves the party")
var notifyOnFill = flag.Bool("notify-fill", true, "Send a notification when the party is filled")
var notifyOnDisband = flag.Bool("notify-disband", false, "Send a notification when the party is disbanded")

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
		log.Println("time: ", err)
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
			if *notifyOnFill && strings.Contains(logLine.Line, "have been filled") {
				return &Notification{
					Title:   "Your Party Has Filled",
					Message: logLine.Line,
					Sound:   "gamelan",
				}
			} else if *notifyOnDisband && strings.Contains(logLine.Line, "has been disbanded") {
				return &Notification{
					Title:   "Your Party Has Disbanded",
					Message: logLine.Line,
					Sound:   "none",
				}
			}
		}
	case 8761: // join/leave/return to party
		{
			if *notifyOnJoin && strings.Contains(logLine.Line, "joins the party") {
				return &Notification{
					Title:   "Player Joined Your Party",
					Message: addSpaceAfterCapitals(logLine.Line),
					Sound:   "none",
				}
			} else if *notifyOnLeave && strings.Contains(logLine.Line, "left the party") {
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
		"token":   appToken,
		"user":    *pushoverUserKey,
		"title":   notification.Title,
		"message": notification.Message,
		"sound":   notification.Sound,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Println("json encode: ", err)
		return
	}
	if _, err := http.Post(messageUrl, "application/json", bytes.NewReader(jsonData)); err != nil {
		log.Println("http: ", err)
		return
	}
	log.Printf("Sent notification: %s", notification.Title)
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: *serverAddress, Path: "MiniParse"}

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	log.Printf("Connected to websocket server at %s.", u.String())

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, rawMessage, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			message, err := decodeMessage(rawMessage)
			if err != nil {
				log.Println("decode: ", err)
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
