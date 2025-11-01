package main

import (
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
)

// Broadcaster holds all the active rooms and a mutex to protect access
type Broadcaster struct {
	// We use a RWMutex to allow multiple goroutines to read the map
	// (e.g., broadcasting) but only one to write (e.g., adding/removing clients).
	lock sync.RWMutex

	// rooms is a map where the key is the room name and the value is
	// another map (acting as a "set") of connected clients.
	// map[roomName]map[*websocket.Conn]bool
	rooms map[string]map[*websocket.Conn]bool
}

// NewBroadcaster creates a new Broadcaster instance
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		rooms: make(map[string]map[*websocket.Conn]bool),
	}
}

// upgrader is used to "upgrade" an HTTP request to a WebSocket connection.
// We set CheckOrigin to true to allow all cross-origin requests.
// In a real production app, you'd want to restrict this!
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// handleWebSocket is the main HTTP handler for WebSocket connections
func (b *Broadcaster) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 1. Get the room name from the URL query (e.g., /ws?room=my-room)
	roomName := r.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "Room name is required", http.StatusBadRequest)
		return
	}

	// 2. Upgrade the HTTP connection to a WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading connection: %v", err)
		return
	}
	// Ensure the connection is closed when the function exits
	defer conn.Close()

	// 3. Add the new client to the room (write-lock the map)
	b.lock.Lock()
	// If the room doesn't exist, create it
	if _, ok := b.rooms[roomName]; !ok {
		b.rooms[roomName] = make(map[*websocket.Conn]bool)
	}
	// Add the client to the room
	b.rooms[roomName][conn] = true
	b.lock.Unlock()

	log.Printf("Client connected to room: %s. Total clients: %d", roomName, len(b.rooms[roomName]))

	// 4. Set up a deferred function to remove the client on disconnect
	defer func() {
		b.lock.Lock()
		// Remove the client from the room
		delete(b.rooms[roomName], conn)

		// If the room is now empty, delete the room itself
		if len(b.rooms[roomName]) == 0 {
			delete(b.rooms, roomName)
			log.Printf("Room %s is now empty and has been deleted.", roomName)
		}
		b.lock.Unlock()
		log.Printf("Client disconnected from room: %s", roomName)
	}()

	// 5. Start the message relay loop
	for {
		// Read a message from the client
		// This will block until a message is received or an error occurs (like disconnect)
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			// If there's an error (e.g., client closed connection), break the loop
			// The deferred function will handle cleanup.
			break
		}

		// We use a read-lock to safely read the list of clients
		b.lock.RLock()

		// Loop over all clients in the *same room*
		for client := range b.rooms[roomName] {
			// Don't send the message back to the sender
			if client != conn {
				// Write the message to the other client
				err := client.WriteMessage(messageType, message)
				if err != nil {
					// If we get an error writing, that client has probably disconnected.
					// Their own read loop will catch this and clean them up.
					log.Printf("Error writing message to client: %v", err)
				}
			}
		}

		b.lock.RUnlock()
	}
}

func main() {
	// Create our one and only broadcaster instance
	broadcaster := NewBroadcaster()

	// Set the handler for the /ws endpoint
	http.HandleFunc("/ws", broadcaster.handleWebSocket)

	// Get the PORT from the environment (this is crucial for Render)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default to 8080 for local testing
	}

	log.Printf("Starting WebSocket server on port %s", port)

	// Start the HTTP server
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
