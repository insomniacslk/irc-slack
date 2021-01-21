package ircslack

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelsNewChannels(t *testing.T) {
	u := NewChannels()
	require.NotNil(t, u)
	assert.NotNil(t, u.channels)
}

type fakeErrorChannelsPaginationComplete struct{}

type fakeChannelsResponse struct {
	Members []slack.Channel
	Channel slack.Channel
}

func (f fakeErrorChannelsPaginationComplete) Error() string {
	return "pagination complete"
}

type fakeSlackHTTPClientChannels struct{}

func (c fakeSlackHTTPClientChannels) Do(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "/api/conversations.list":
		// reply as per https://api.slack.com/methods/channels.list
		data := []byte(`{"channels": [{"id": "1234", "name": "general", "is_channel": true}], "response_metadata": {"next_cursor": ""}}`)
		return &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       ioutil.NopCloser(bytes.NewBuffer(data)),
		}, nil
	default:
		return nil, fmt.Errorf("testing: http client URL not supported: %s", req.URL)
	}
}

func TestChannelsFetch(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClientChannels{}))
	channels := NewChannels()
	err := channels.Fetch(client)
	require.NoError(t, err)
	assert.Equal(t, 1, channels.Count())
}

func TestChannelsById(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClientChannels{}))
	channels := NewChannels()
	err := channels.Fetch(client)
	require.NoError(t, err)
	u := channels.ByID("1234")
	require.NotNil(t, u)
	assert.Equal(t, "1234", u.ID)
	assert.Equal(t, "general", u.Name)
}

func TestChannelsByName(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClientChannels{}))
	channels := NewChannels()
	err := channels.Fetch(client)
	require.NoError(t, err)
	u := channels.ByName("general")
	require.NotNil(t, u)
	assert.Equal(t, "1234", u.ID)
	assert.Equal(t, "general", u.Name)
}
