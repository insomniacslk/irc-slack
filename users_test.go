package main

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

func TestUsersNewUsers(t *testing.T) {
	u := NewUsers(0)
	require.NotNil(t, u)
	assert.NotNil(t, u.users)
	assert.Equal(t, 0, u.pagination)

	u = NewUsers(200)
	require.NotNil(t, u)
	assert.NotNil(t, u.users)
	assert.Equal(t, 200, u.pagination)
}

type fakeErrorUsersPaginationComplete struct{}

type fakeUsersResponse struct {
	Members []slack.User
	User    slack.User
}

func (f fakeErrorUsersPaginationComplete) Error() string {
	return "pagination complete"
}

type fakeSlackHTTPClient struct{}

func (c fakeSlackHTTPClient) Do(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "/api/users.list":
		// reply as per https://api.slack.com/methods/users.list
		data := []byte(`{"ok": true, "members": [{"id": "UABCD", "name": "insomniac"}], "response_metadata": {"next_cursor": ""}}`)
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

func TestUsersFetch(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClient{}))
	users := NewUsers(10)
	err := users.Fetch(client)
	require.NoError(t, err)
	assert.Equal(t, 1, users.Count())
}

func TestUsersById(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClient{}))
	users := NewUsers(10)
	err := users.Fetch(client)
	require.NoError(t, err)
	u := users.ByID("UABCD")
	require.NotNil(t, u)
	assert.Equal(t, "UABCD", u.ID)
	assert.Equal(t, "insomniac", u.Name)
}

func TestUsersByName(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClient{}))
	users := NewUsers(10)
	err := users.Fetch(client)
	require.NoError(t, err)
	u := users.ByName("insomniac")
	require.NotNil(t, u)
	assert.Equal(t, "UABCD", u.ID)
	assert.Equal(t, "insomniac", u.Name)
}

func TestUsersIDsToNames(t *testing.T) {
	client := slack.New("test-token", slack.OptionHTTPClient(fakeSlackHTTPClient{}))
	users := NewUsers(10)
	err := users.Fetch(client)
	require.NoError(t, err)
	names := users.IDsToNames("UABCD")
	assert.Equal(t, []string{"insomniac"}, names)
}
