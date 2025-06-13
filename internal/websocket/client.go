package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Manager struct {
	clients      map[*websocket.Conn]bool
	clientsMutex sync.RWMutex
	upgrader     websocket.Upgrader
}

type LogMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type StatusUpdate struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func NewManager() *Manager {
	return &Manager{
		clients: make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (m *Manager) HandleConnection(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return nil, err
	}

	m.clientsMutex.Lock()
	m.clients[conn] = true
	m.clientsMutex.Unlock()

	return conn, nil
}

func (m *Manager) RemoveConnection(conn *websocket.Conn) {
	m.clientsMutex.Lock()
	defer m.clientsMutex.Unlock()
	delete(m.clients, conn)
}

func (m *Manager) BroadcastMessage(message interface{}) {
	m.clientsMutex.RLock()
	defer m.clientsMutex.RUnlock()

	jsonData, _ := json.Marshal(message)
	for client := range m.clients {
		err := client.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			client.Close()
			delete(m.clients, client)
		}
	}
}

func (m *Manager) SendLog(message string) {
	logMsg := LogMessage{
		Type:      "log",
		Message:   message,
		Timestamp: time.Now().Format("15:04:05"),
	}
	m.BroadcastMessage(logMsg)
}

func (m *Manager) UpdateStatus(status string) {
	statusMsg := StatusUpdate{
		Type:   "status",
		Status: status,
	}
	m.BroadcastMessage(statusMsg)
}