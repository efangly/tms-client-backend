package services

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"tms-backend/internal/utils"
)

// MQTTTemperaturePayload represents the temperature data sent via MQTT
type MQTTTemperaturePayload struct {
	MachineName string  `json:"machineName"`
	TempValue   float64 `json:"tempValue"`
	Status      string  `json:"status"` // N=Normal, H=High, L=Low
	Timestamp   string  `json:"timestamp"`
}

// MQTTService handles MQTT connection and publishing
type MQTTService struct {
	client   mqtt.Client
	broker   string
	port     string
	clientID string
	username string
	password string
	topic    string
	enabled  bool
	mu       sync.Mutex
}

// Global MQTT service instance
var GlobalMQTTService *MQTTService

// NewMQTTService creates a new MQTT service from environment variables
func NewMQTTService() *MQTTService {
	broker := os.Getenv("MQTT_BROKER")
	port := os.Getenv("MQTT_PORT")
	clientID := os.Getenv("MQTT_CLIENT_ID")
	username := os.Getenv("MQTT_USERNAME")
	password := os.Getenv("MQTT_PASSWORD")
	topic := os.Getenv("MQTT_TOPIC")

	if broker == "" {
		return &MQTTService{enabled: false}
	}

	if port == "" {
		port = "1883"
	}

	if clientID == "" {
		clientID = fmt.Sprintf("tms-backend-%d", time.Now().UnixNano())
	}

	if topic == "" {
		topic = "tms/temperature"
	}

	return &MQTTService{
		broker:   broker,
		port:     port,
		clientID: clientID,
		username: username,
		password: password,
		topic:    topic,
		enabled:  true,
	}
}

// Connect establishes connection to the MQTT broker
func (m *MQTTService) Connect() error {
	if !m.enabled {
		log.Println("📡 MQTT: DISABLED (MQTT_BROKER not configured)")
		return nil
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%s", m.broker, m.port))
	opts.SetClientID(m.clientID)

	if m.username != "" {
		opts.SetUsername(m.username)
	}
	if m.password != "" {
		opts.SetPassword(m.password)
	}

	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetCleanSession(true)

	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		utils.LogError("MQTT connection lost: %v", err)
		log.Printf("⚠️  MQTT connection lost: %v", err)
	})

	opts.SetOnConnectHandler(func(client mqtt.Client) {
		log.Println("✅ MQTT reconnected to broker")
	})

	m.client = mqtt.NewClient(opts)

	token := m.client.Connect()
	if token.Wait() && token.Error() != nil {
		utils.LogError("MQTT connect failed: %v", token.Error())
		return fmt.Errorf("MQTT connect failed: %v", token.Error())
	}

	log.Printf("✅ MQTT connected to %s:%s (clientID: %s)", m.broker, m.port, m.clientID)
	log.Printf("   Topic: %s", m.topic)
	return nil
}

// Disconnect closes the MQTT connection
func (m *MQTTService) Disconnect() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(1000)
		log.Println("📡 MQTT disconnected")
	}
}

// IsEnabled returns whether MQTT is configured and enabled
func (m *MQTTService) IsEnabled() bool {
	return m.enabled
}

// IsConnected returns whether MQTT client is currently connected
func (m *MQTTService) IsConnected() bool {
	return m.enabled && m.client != nil && m.client.IsConnected()
}

// PublishTemperature publishes a single temperature reading to MQTT
func (m *MQTTService) PublishTemperature(payload MQTTTemperaturePayload) error {
	if !m.IsConnected() {
		return fmt.Errorf("MQTT not connected")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal MQTT payload: %v", err)
	}

	// Publish to topic: tms/temperature
	token := m.client.Publish(m.topic, 0, false, data)
	token.Wait()

	if token.Error() != nil {
		return fmt.Errorf("MQTT publish failed: %v", token.Error())
	}

	return nil
}

// PublishTemperatureBatch publishes multiple temperature readings to MQTT
func (m *MQTTService) PublishTemperatureBatch(payloads []MQTTTemperaturePayload) error {
	if !m.IsConnected() {
		return fmt.Errorf("MQTT not connected")
	}

	// Publish all readings as a single batch message
	data, err := json.Marshal(payloads)
	if err != nil {
		return fmt.Errorf("failed to marshal MQTT batch payload: %v", err)
	}

	topic := fmt.Sprintf("%s/batch", m.topic)
	token := m.client.Publish(topic, 0, false, data)
	token.Wait()

	if token.Error() != nil {
		return fmt.Errorf("MQTT batch publish failed: %v", token.Error())
	}

	return nil
}
