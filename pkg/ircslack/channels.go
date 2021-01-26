package ircslack

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// Channels wraps the channel list with convenient operations and cache.
type Channels struct {
	channels   map[string]Channel
	Pagination int
	mu         sync.Mutex
}

// NewChannels creates a new Channels object.
func NewChannels(pagination int) *Channels {
	return &Channels{
		channels:   make(map[string]Channel),
		Pagination: pagination,
	}
}

// SupportedChannelPrefixes returns a list of supported channel prefixes.
func SupportedChannelPrefixes() []string {
	return []string{
		ChannelPrefixPublicChannel,
		ChannelPrefixPrivateChannel,
		ChannelPrefixMpIM,
		ChannelPrefixThread,
	}

}

// AsMap returns the channels as a map of name -> channel. The map is copied to
// avoid data races
func (c *Channels) AsMap() map[string]Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	ret := make(map[string]Channel, len(c.channels))
	for k, v := range c.channels {
		ret[k] = v
	}
	return ret
}

// FetchByIDs fetches the channels with the specified IDs and updates the
// internal channel mapping.
func (c *Channels) FetchByIDs(client *slack.Client, skipCache bool, channelIDs ...string) ([]Channel, error) {
	var (
		toRetrieve       []string
		alreadyRetrieved []Channel
	)

	if !skipCache {
		c.mu.Lock()
		for _, cid := range channelIDs {
			if ch, ok := c.channels[cid]; !ok {
				toRetrieve = append(toRetrieve, cid)
			} else {
				alreadyRetrieved = append(alreadyRetrieved, ch)
			}
		}
		c.mu.Unlock()
		log.Debugf("Fetching information for %d channels out of %d (%d already in cache)", len(toRetrieve), len(channelIDs), len(channelIDs)-len(toRetrieve))
	} else {
		toRetrieve = channelIDs
	}
	allFetchedChannels := make([]Channel, 0, len(channelIDs))
	for i := 0; i < len(toRetrieve); i++ {
		for {
			attempt := 0
			if attempt >= MaxSlackAPIAttempts {
				return nil, fmt.Errorf("Channels.FetchByIDs: exceeded the maximum number of attempts (%d) with the Slack API", MaxSlackAPIAttempts)
			}
			log.Debugf("Fetching %d channels of %d, attempt %d of %d", len(toRetrieve), len(channelIDs), attempt+1, MaxSlackAPIAttempts)
			slackChannel, err := client.GetConversationInfo(toRetrieve[i], true)
			if err != nil {
				if rlErr, ok := err.(*slack.RateLimitedError); ok {
					// we were rate-limited. Let's wait the recommended delay
					log.Warningf("Hit Slack API rate limiter. Waiting %v", rlErr.RetryAfter)
					time.Sleep(rlErr.RetryAfter)
					attempt++
					continue
				}
				return nil, err
			}
			ch := Channel(*slackChannel)
			allFetchedChannels = append(allFetchedChannels, ch)
			// also update the local users map
			c.mu.Lock()
			c.channels[ch.ID] = ch
			c.mu.Unlock()
			break
		}
	}
	allChannels := append(alreadyRetrieved, allFetchedChannels...)
	if len(channelIDs) != len(allChannels) {
		return allFetchedChannels, fmt.Errorf("Found %d users but %d were requested", len(allChannels), len(channelIDs))
	}
	return allChannels, nil
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
		channels = make(map[string]Channel)
	)
	start := time.Now()
	params := slack.GetConversationsParameters{
		Types: []string{"public_channel", "private_channel"},
		Limit: c.Pagination,
	}
	for err == nil {
		chans, nextCursor, err := client.GetConversationsContext(ctx, &params)
		if err == nil {
			log.Debugf("Retrieved %d channels (current total is %d)", len(chans), len(channels))
			for _, sch := range chans {
				// WARNING WARNING WARNING: channels are internally mapped by
				// the Slack name, while users are mapped by Slack ID.
				ch := Channel(sch)
				channels[ch.SlackName()] = ch
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
func (c *Channels) ByID(id string) *Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, c := range c.channels {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// ByName retrieves a channel by its Slack or IRC name.
func (c *Channels) ByName(name string) *Channel {
	if HasChannelPrefix(name) {
		// without prefix, the channel now has the form of a Slack name
		name = name[1:]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.channels[name]; ok {
		return &ch
	}
	return nil
}
