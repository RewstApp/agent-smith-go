package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

const maxBandwidthMbps = 100 // change based on your NIC or plan

func (status *Status) GetDeviceStatus() {
	// Get the CPU usage percent
	percent, err := cpu.Percent(time.Second, false)
	if err != nil {
		log.Println("Failed to get cpu percent:", err)
		return
	}
	status.Cpu = int(percent[0])

	// Get total memory usage
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		log.Println("Failed to get memory usage:", err)
	}
	status.Memory = int(vmStat.UsedPercent)

	// Get disk usage
	usage, err := disk.Usage("C:\\") // "/" for Linux/macOS, "C:\\" for Windows
	if err != nil {
		log.Println("Failed to get disk usage:", err)
	}
	status.Disk = int(usage.UsedPercent)

	// Get network usage
	initial, _ := net.IOCounters(false)
	time.Sleep(1 * time.Second)
	current, _ := net.IOCounters(false)

	bytesSent := current[0].BytesSent - initial[0].BytesSent
	bytesRecv := current[0].BytesRecv - initial[0].BytesRecv

	// Total used bandwidth in Mbps (Megabits per second)
	totalBits := float64((bytesSent + bytesRecv) * 8)
	mbps := totalBits / (1024 * 1024)

	// Compute usage percentage
	usagePercent := (mbps / maxBandwidthMbps) * 100
	status.Network = int(usagePercent)
}

// Create an Upgrader — this upgrades HTTP connections to WebSocket.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all connections for testing. Be careful in production!
	},
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	// Upgrade the connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()
	log.Println("Client connected:", conn.RemoteAddr())

	var writeMu sync.Mutex

	// Define a write message helper function and wrap it with mutex
	writeMessage := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()

		return conn.WriteMessage(messageType, data)
	}

	for {
		// Read message
		mt, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			break
		}
		log.Printf("Received: %s\n", message)

		// Echo message back
		err = writeMessage(mt, message)
		if err != nil {
			log.Println("Write error:", err)
			break
		}
	}
}

func RunServer(port int) {
	http.HandleFunc("/ws", wsHandler)

	fmt.Printf("WebSocket server listening on :%d/ws\n", port)
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		log.Println("ListenAndServe: " + err.Error())
	}
}
