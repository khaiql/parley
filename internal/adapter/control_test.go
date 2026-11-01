package adapter

import (
	"encoding/json"
	"net"
	"testing"
)

type controlWriteSignal struct {
	net.Conn
	writing chan struct{}
}

func (c controlWriteSignal) Write(data []byte) (int, error) {
	close(c.writing)
	return c.Conn.Write(data)
}

func TestControlShutdownWaitsForResponseWrite(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		name := "response delivered"
		if disconnect {
			name = "client disconnected"
		}
		t.Run(name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			writing := make(chan struct{})
			stopped := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				serveControlConn(controlWriteSignal{Conn: server, writing: writing}, func(ControlRequest) ControlResponse {
					return ControlResponse{OK: true, Status: "stopping", AfterWrite: func() { close(stopped) }}
				})
			}()
			if err := json.NewEncoder(client).Encode(ControlRequest{Type: "stop"}); err != nil {
				t.Fatal(err)
			}
			<-writing
			select {
			case <-stopped:
				t.Fatal("shutdown started before response write completed")
			default:
			}
			if disconnect {
				_ = client.Close()
			} else {
				var response ControlResponse
				if err := json.NewDecoder(client).Decode(&response); err != nil {
					t.Fatal(err)
				}
				if !response.OK || response.Status != "stopping" {
					t.Fatalf("stop response = %+v", response)
				}
			}
			<-done
			select {
			case <-stopped:
			default:
				t.Fatal("shutdown did not start after response write")
			}
		})
	}
}
