package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mdp/qrterminal"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/daemonctl"
)

func runStatus() error {
	base := config.BaseURL()
	if !daemonctl.Healthy(base) {
		fmt.Println("daemon: not running")
		fmt.Printf("data dir: %s\nstart it with: whatsapp-mcp serve (or just use an MCP client — stdio auto-starts it)\n", config.BaseDir())
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var st struct {
		State   string `json:"state"`
		QRCode  string `json:"qr_code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	fmt.Printf("daemon: running (%s)\nwhatsapp: %s\n", base, st.State)
	if st.Message != "" {
		fmt.Println(st.Message)
	}
	if st.QRCode != "" {
		fmt.Println("\nScan this QR code with WhatsApp:")
		qrterminal.GenerateHalfBlock(st.QRCode, qrterminal.L, os.Stdout)
	}
	return nil
}

func runStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := daemonctl.EnsureRunning(exe); err != nil {
		return err
	}
	fmt.Printf("daemon: running (%s)\n", config.BaseURL())
	return nil
}

func runStop() error {
	base := config.BaseURL()
	if !daemonctl.Healthy(base) {
		fmt.Println("daemon: not running")
		return nil
	}
	if err := daemonctl.StopDaemon(base); err != nil {
		return err
	}
	fmt.Println("daemon: stopped")
	return nil
}
