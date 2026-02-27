package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type MessageHandler func(topic string, payload []byte)

type Client struct {
	client   paho.Client
	handlers map[string]MessageHandler
	mu       sync.RWMutex
}

type Config struct {
	Broker   string
	Port     string
	Username string
	Password string
	ClientID string
}

func NewClient(cfg *Config) (*Client, error) {
	opts := paho.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%s", cfg.Broker, cfg.Port))
	opts.SetClientID(cfg.ClientID)
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(false)
	opts.SetConnectTimeout(15 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetCleanSession(false)
	opts.SetOrderMatters(false)

	c := &Client{handlers: make(map[string]MessageHandler)}
	opts.SetDefaultPublishHandler(c.defaultHandler)
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)
	opts.SetReconnectingHandler(c.onReconnecting)
	c.client = paho.NewClient(opts)
	return c, nil
}

func (c *Client) Connect() error {
	token := c.client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("MQTT connection timeout after 15s")
	}
	if token.Error() != nil {
		return token.Error()
	}
	log.Println("MQTT: Connected to broker")
	return nil
}

func (c *Client) Disconnect() {
	c.client.Disconnect(1000)
	log.Println("MQTT: Disconnected")
}

func (c *Client) IsConnected() bool { return c.client.IsConnected() }

func (c *Client) Subscribe(topic string, handler MessageHandler) error {
	c.mu.Lock()
	c.handlers[topic] = handler
	c.mu.Unlock()
	token := c.client.Subscribe(topic, 1, func(client paho.Client, msg paho.Message) {
		c.mu.RLock()
		h, ok := c.handlers[msg.Topic()]
		c.mu.RUnlock()
		if ok {
			h(msg.Topic(), msg.Payload())
		}
	})
	if token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (c *Client) Unsubscribe(topics ...string) error {
	token := c.client.Unsubscribe(topics...)
	if token.Wait() && token.Error() != nil {
		return token.Error()
	}
	c.mu.Lock()
	for _, topic := range topics {
		delete(c.handlers, topic)
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) Publish(topic string, payload []byte) error {
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (c *Client) PublishJSON(topic string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.Publish(topic, payload)
}

func (c *Client) PublishRetained(topic string, payload []byte) error {
	token := c.client.Publish(topic, 1, true, payload)
	if token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (c *Client) defaultHandler(client paho.Client, msg paho.Message) {
	log.Printf("MQTT: Unhandled message on %s", msg.Topic())
}

func (c *Client) onConnect(client paho.Client) {
	log.Println("MQTT: Connected")
	c.mu.RLock()
	defer c.mu.RUnlock()
	for topic := range c.handlers {
		c.client.Subscribe(topic, 1, nil)
	}
}

func (c *Client) onConnectionLost(client paho.Client, err error) {
	log.Printf("MQTT: Connection lost: %v", err)
}

func (c *Client) onReconnecting(client paho.Client, opts *paho.ClientOptions) {
	log.Println("MQTT: Reconnecting...")
}
