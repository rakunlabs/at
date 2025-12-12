package server

import (
	"github.com/oklog/ulid/v2"
)

type MessageChannel struct {
	Type  string
	Value string
}

func (s *Server) addClient() (string, <-chan MessageChannel) {
	s.m.Lock()
	defer s.m.Unlock()

	messageChan := make(chan MessageChannel, 64)

	key := ulid.Make().String()
	s.channels[key] = messageChan

	return key, messageChan
}

func (s *Server) deleteClient(keys ...string) {
	s.m.Lock()
	defer s.m.Unlock()

	for _, k := range keys {
		delete(s.channels, k)
	}
}

func (s *Server) broadcastMessage(message MessageChannel) {
	deleteClients := make([]string, 0, 10)
	defer func() {
		s.deleteClient(deleteClients...)
	}()

	s.m.RLock()
	defer s.m.RUnlock()

	for key := range s.channels {
		select {
		case s.channels[key] <- message:
		default:
			deleteClients = append(deleteClients, key)
		}
	}
}
