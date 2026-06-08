package collector

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// StartWazuhForwarder lê o arquivo eve.json do Suricata (montado via volume) e envia para o Wazuh
func StartWazuhForwarder(eveLogPath, wazuhIP, wazuhPort string) {
	if wazuhIP == "" {
		log.Println("WAZUH_MANAGER_IP not set, skipping Wazuh forwarder")
		return
	}

	wazuhAddr := fmt.Sprintf("%s:%s", wazuhIP, wazuhPort)
	
	// Tentativa de conexão contínua com o Wazuh
	var conn net.Conn
	var err error
	for {
		conn, err = net.Dial("udp", wazuhAddr) // Ou tcp, dependendo da config do Wazuh (Syslog)
		if err == nil {
			break
		}
		log.Printf("Waiting for Wazuh manager at %s...", wazuhAddr)
		time.Sleep(5 * time.Second)
	}
	defer conn.Close()

	log.Printf("Connected to Wazuh Manager at %s", wazuhAddr)

	file, err := os.Open(eveLogPath)
	if err != nil {
		log.Fatalf("Error opening Suricata eve.json: %v", err)
	}
	defer file.Close()

	// Move o ponteiro para o final do arquivo para simular o "tail -f"
	file.Seek(0, 2)
	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			time.Sleep(1 * time.Second) // Aguarda novos logs se chegou ao EOF
			continue
		}

		// Envia o JSON puro do Suricata como payload do Syslog para o Wazuh
		syslogMsg := fmt.Sprintf("<132>Suricata: %s", line)
		_, err = conn.Write([]byte(syslogMsg))
		if err != nil {
			log.Printf("Error sending to Wazuh: %v", err)
		}
	}
}
