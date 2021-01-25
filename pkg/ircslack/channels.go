package ircslack

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// Channels wraps the channel list with convenient operations and cache.
type Channels struct {
	channels   map[string]slack.Channel
	Pagination int
	mu         sync.Mutex
}

// NewChannels creates a new Channels object.
func NewChannels(pagination int) *Channels {
	return &Channels{
		channels:   make(map[string]slack.Channel),
		Pagination: pagination,
	}
}

// AsMap returns the channels as a map of name -> channel. The map is copied to
// avoid data races
func (c *Channels) AsMap() map[string]slack.Channel {
	var ret map[string]slack.Channel
	for k, v := range c.channels {
		ret[k] = v
	}
	return ret
}

// Fetch retrieves all the channels on a given Slack team. The Slack client has
// to be valid and connected.
func (c *Channels) Fetch(client *slack.Client) error {
	log.Infof("Fetching all channels, might take a while on large Slack teams")
	// currently slack-go does not expose a way to change channel pagination as
	// it does for the users API.
	var (
		err      error
		ctx      = context.Background()
		channels = make(map[string]slack.Channel)
	)
	start := time.Now()
	params := slack.GetConversationsParameters{
		Limit: c.Pagination,
	}
	for err == nil {
		chans, nextCursor, err := client.GetConversationsContext(ctx, &params)
		if err == nil {
			log.Debugf("Retrieved %d channels (current total is %d)", len(chans), len(channels))
			for _, c := range chans {
				// WARNING WARNING WARNING: channels are internally mapped by
				// name, while users are mapped by ID.
				channels[c.Name] = c
			}
		} else if rateLimitedError, ok := err.(*slack.RateLimitedError); ok {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(rateLimitedError.RetryAfter):
				err = nil
			}
		}
		if nextCursor == "" {
			break
		}
		params.Cursor = nextCursor
	}
	log.Infof("Retrieved %d channels in %s", len(channels), time.Since(start))
	c.mu.Lock()
	c.channels = channels
	for name, ch := range channels {
		log.Debugf("Retrieved channel: %s -> %+v", name, ch)
	}
	c.mu.Unlock()
	return nil
}

// Count returns the number of channels. This method must be called after
// `Fetch`.
func (c *Channels) Count() int {
	return len(c.channels)
}

// ByID retrieves a channel by its Slack ID.
func (c *Channels) ByID(id string) *slack.Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, c := range c.channels {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// ByName retrieves a channel by its Slack name.
func (c *Channels) ByName(name string) *slack.Channel {
	if strings.HasPrefix(name, "#") {
		name = name[1:]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, c := range c.channels {
		if c.Name == name {
			return &c
		}
	}
	return nil
}
