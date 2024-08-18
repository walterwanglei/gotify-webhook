package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gotify/plugin-api"
)

// GetGotifyPluginInfo returns gotify plugin info.
func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		ModulePath:  "github.com/wuxs/gotify-webhook",
		Author:      "wuxs",
		Version:     "0.1.0",
		Description: "forward message to others webhook server",
		Name:        "WebHook",
	}
}

// EchoPlugin is the gotify plugin instance.
type MultiNotifierPlugin struct {
	msgHandler     plugin.MessageHandler
	storageHandler plugin.StorageHandler
	config         *Config
	basePath       string
	done           chan struct{}
}

func (p *MultiNotifierPlugin) TestSocket(serverUrl string) (err error) {
	_, _, err = websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		log.Println("Test dial error : ", err)
		return err
	}
	return nil
}

// Enable enables the plugin.
func (p *MultiNotifierPlugin) Enable() error {
	if len(p.config.HostServer) < 1 {
		return errors.New("please enter the correct web server")
	}
	p.done = make(chan struct{})
	log.Println("echo plugin enabled")
	serverUrl := p.config.HostServer + "/stream?token=" + p.config.ClientToken
	log.Println("Websocket url : ", serverUrl)
	go p.ReceiveMessages(serverUrl)
	return nil
}

// Disable disables the plugin.
func (p *MultiNotifierPlugin) Disable() error {
	log.Println("echo plugin disbled")
	close(p.done)
	return nil
}

// SetStorageHandler implements plugin.Storager
func (p *MultiNotifierPlugin) SetStorageHandler(h plugin.StorageHandler) {
	p.storageHandler = h
}

// SetMessageHandler implements plugin.Messenger.
func (p *MultiNotifierPlugin) SetMessageHandler(h plugin.MessageHandler) {
	p.msgHandler = h
}

// Storage defines the plugin storage scheme
type Storage struct {
	CalledTimes int `json:"called_times"`
}

type MessageExtras struct {
	Id    int
	Appid int
}

type Rule struct {
	Type  string   `yaml:"type"`
	Mode  string   `yaml:"mode"`
	Texts []string `yaml:"texts"`
}

type WebHook struct {
	Url    string            `yaml:"url"`
	Method string            `yaml:"method"`
	Body   string            `yaml:"body"`
	Header map[string]string `yaml:"header"`
	Tags   []string          `yaml:"tags"`
	Rules  []*Rule           `yaml:"rules"`
}

// Config defines the plugin config scheme
type Config struct {
	ClientToken string     `yaml:"client_token" validate:"required"`
	HostServer  string     `yaml:"host_server" validate:"required"`
	Debug       bool       `yaml:"debug" validate:"required"`
	WebHooks    []*WebHook `yaml:"web_hooks"`
}

// DefaultConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) DefaultConfig() interface{} {
	c := &Config{
		ClientToken: "CrMo3UaAQG1H37G",
		HostServer:  "ws://localhost",
		Debug:       false,
	}
	return c
}

// ValidateAndSetConfig implements plugin.Configurer
func (p *MultiNotifierPlugin) ValidateAndSetConfig(config interface{}) error {
	p.config = config.(*Config)
	return nil
}

// GetDisplay implements plugin.Displayer.
func (p *MultiNotifierPlugin) GetDisplay(location *url.URL) string {
	message := `
	如何填写配置：

	1. 创建一个新的 Client，获取 token，更新配置中的 client_token
	2. 修改 gotify 服务器地址，默认为 ws://localhost
	3. 填写需要接受通知的 webhook 配置

	webhook 示例:
	web_hooks:
	  - url: http://192.168.1.2:10201/api/sendTextMsg
		method: POST
		body: "{\"wxid\":\"xxxxxxxx\",\"msg\":\"$title\n$message\"}"
	  - url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxx"
		method: "POST"
		body: "{\"msgtype\":\"text\",\"text\":{\"content\":\"$title\n$message\"}}"

	注：请在更改后重新启用插件。
	`
	return message
}

func (p *MultiNotifierPlugin) CheckMessage(msg plugin.Message, webhook *WebHook, extras MessageExtras) (bool, error) {
	var matchTag = false
	if len(webhook.Rules) == 0 {
		var msgTag = ""
		if msg.Extras == nil {
			if p.config.Debug {
				log.Printf("msg.Extras is null")
			}
		} else {
			if val, ok := msg.Extras["tag"]; ok {
				msgTag = val.(string)
			}
			if p.config.Debug {
				log.Printf("msgTag : %v", msgTag)
			}
			for _, tag := range webhook.Tags {
				if p.config.Debug {
					log.Printf("tag : %v", tag)
				}
				if msgTag != "" && msgTag == tag {
					matchTag = true
					break
				}
			}
		}
	} else {
		matchTag = true
		for _, rule := range webhook.Rules {
			if rule.Type == "" {
				rule.Type = "appid"
			}
			if rule.Mode == "" {
				rule.Mode = "&&"
			}
			if len(rule.Texts) == 0 {
				if p.config.Debug {
					log.Printf("len(rule.Texts) == 0")
				}
				matchTag = false
				break
			} else {
				var str = ""
				var compare = "eq"
				if p.config.Debug {
					log.Printf("rule.Type : %v", rule.Type)
					log.Printf("rule.Mode : %v", rule.Mode)
					log.Printf("rule.Texts : %v", rule.Texts)
				}
				if rule.Type == "appid" {
					if p.config.Debug {
						log.Printf("extras.Appid : %v", extras.Appid)
					}
					str = fmt.Sprintf("%d", extras.Appid)
				} else if rule.Type == "title" {
					str = msg.Title
					compare = "in"
				} else if rule.Type == "message" {
					str = msg.Message
					compare = "in"
				} else if rule.Type == "tag" {
					if msg.Extras != nil {
						if val, ok := msg.Extras["tag"]; ok {
							str = val.(string)
						}
					}
				}
				if p.config.Debug {
					log.Printf("compare : %v", compare)
					log.Printf("str : %v", str)
				}
				if rule.Mode != "&&" {
					matchTag = false
				}
				for _, text := range rule.Texts {
					var flag = false
					switch compare {
					case "eq":
						flag = strings.EqualFold(str, text)
					case "in":
						flag = strings.Contains(str, text)
					}
					if rule.Mode == "&&" {
						matchTag = matchTag && flag
						if !matchTag {
							break
						}
					} else {
						matchTag = matchTag || flag
					}
				}
			}
		}
	}
	return matchTag, nil
}

func (p *MultiNotifierPlugin) SendMessage(msg plugin.Message, webhook *WebHook, extras MessageExtras) (err error) {
	matchFlag, err := p.CheckMessage(msg, webhook, extras)
	if err != nil {
		log.Printf("CheckMessage error : %v ", err)
		return err
	}
	if !matchFlag {
		log.Printf("msg dont match, skip")
		return nil
	}
	if webhook.Url == "" {
		return errors.New("webhook url is empty")
	}
	if webhook.Method == "" {
		webhook.Method = "POST"
	}
	if webhook.Header == nil {
		webhook.Header = map[string]string{
			"Content-Type": "application/json",
		}
	}
	if p.config.Debug {
		log.Printf("webhook Header : %v", webhook.Header)
	}
	if webhook.Body == "" {
		webhook.Body = "{\"msg\":\"$title\n$message\"}"
	}
	body := webhook.Body
	body = strings.Replace(body, "$title", msg.Title, -1)
	body = strings.Replace(body, "$message", msg.Message, -1)
	body = strings.Replace(body, "\r", "\\r", -1)
	body = strings.Replace(body, "\n", "\\n", -1)
	if p.config.Debug {
		log.Printf("webhook body : %s", body)
	}
	payload := strings.NewReader(body)
	req, err := http.NewRequest(webhook.Method, webhook.Url, payload)
	if err != nil {
		log.Printf("NewRequest error : %v ", err)
		return err
	}
	for k, v := range webhook.Header {
		req.Header.Add(k, v)
		if p.config.Debug {
			log.Printf("Add Header : %v = %v", k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Do request error : %v ", err)
		return err
	}
	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("Read response error : %v ", err)
		return err
	}
	if p.config.Debug {
		log.Printf("webhook response : %v ", string(resBody))
	}
	return
}

func (p *MultiNotifierPlugin) ReceiveMessages(serverUrl string) {
	time.Sleep(1 * time.Second)

	err := p.receiveMessages(serverUrl)
	if err != nil {
		log.Println("read message error, retry after 1s")
	}
}

func (p *MultiNotifierPlugin) receiveMessages(serverUrl string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	conn, _, err := websocket.DefaultDialer.Dial(serverUrl, nil)
	if err != nil {
		log.Println("Dial error : ", err)
		return err
	}
	log.Printf("Connected to %s", serverUrl)
	defer conn.Close()
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("Websocket read message error :", err)
				return
			}
			if p.config.Debug {
				log.Printf("Websocket read message : %s", message)
			}
			if message[0] == '{' {
				msg := plugin.Message{}
				if err := json.Unmarshal(message, &msg); err != nil {
					log.Println("Json Unmarshal error :", err)
					continue
				}
				msgExtras := MessageExtras{}
				if err := json.Unmarshal(message, &msgExtras); err != nil {
					log.Println("Json Unmarshal error :", err)
				}
				for _, webhook := range p.config.WebHooks {
					err = p.SendMessage(msg, webhook, msgExtras)
					if err != nil {
						log.Printf("SendMessage error : %v ", err)
					}
				}
			} else {
				log.Println("unsupported message format")
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			log.Println("plugin stopped")
			return
		case t := <-ticker.C:
			err := conn.WriteMessage(websocket.TextMessage, []byte(t.String()))
			if err != nil {
				log.Println("write:", err)
				return err
			}
			ticker.Reset(time.Second)
		case <-interrupt:
			log.Println("plugin interrupt")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return err
			}
			_ = conn.Close()
			return err
		}
	}
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return &MultiNotifierPlugin{}
}

//func main() {
//	panic("this should be built as go plugin")
//}
