package api

// HubPublisher adapts the SSE hub to the collector's Publisher interface, so
// the collector broadcasts without importing the api package.
type HubPublisher struct{ Hub *Hub }

// PublishEvent broadcasts a recorded event.
func (p HubPublisher) PublishEvent(payload any) {
	p.Hub.Publish(Message{Event: "event", Data: payload})
}

// PublishChange broadcasts a configuration change.
func (p HubPublisher) PublishChange(payload any) {
	p.Hub.Publish(Message{Event: "snapshot.changed", Data: payload})
}
