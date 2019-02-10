package main

import (
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/nlopes/slack"
)

const (
	maxHTTPAttempts = 3
	retryInterval   = time.Second
)

// FileHandler downloads files from slack
type FileHandler struct {
	SlackAPIKey          string
	FileDownloadLocation string
	ProxyPrefix          string
}

func retryableNetError(err error) bool {
	if err == nil {
		return false
	}
	switch err := err.(type) {
	case net.Error:
		if err.Timeout() {
			return true
		}
	}
	return false
}

func retryableHTTPError(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode == 500 || resp.StatusCode == 502 {
		return true
	}
	return false
}

// Download downloads url contents to a local file and returns a url to either
// the file on slack's server or a downloaded file
func (handler *FileHandler) Download(file slack.File) string {
	fileURL := file.URLPrivate
	if handler.FileDownloadLocation == "" || file.IsExternal || handler.SlackAPIKey == "" {
		return fileURL
	}
	localFileName := fmt.Sprintf("%s_%s.%s", file.ID, file.Title, file.Filetype)
	localFilePath := filepath.Join(handler.FileDownloadLocation, localFileName)
	go func() {
		out, err := os.Create(localFilePath)
		if err != nil {
			log.Printf("Could not create file for download %s: %v", localFilePath, err)
			return
		}

		defer out.Close()
		request, _ := http.NewRequest("GET", fileURL, nil)
		request.Header.Add("Authorization", "Bearer "+handler.SlackAPIKey)
		var client = &http.Client{}
		var resp *http.Response
		for attempt := 0; attempt < maxHTTPAttempts; attempt++ {
			resp, err = client.Do(request)
			if err != nil && retryableNetError(err) || retryableHTTPError(resp) {
				time.Sleep(retryInterval * time.Duration(math.Pow(float64(attempt), 2)))
				continue
			}
			if err == nil {
				break
			}
			log.Printf("Error downloading %s: %v", fileURL, err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			log.Printf("Got %d while downloading %s", resp.StatusCode, fileURL)
			return
		}
		defer resp.Body.Close()
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			log.Printf("Error writing %s: %v", fileURL, err)
		}
		return
	}()
	if handler.ProxyPrefix != "" {
		return handler.ProxyPrefix + url.PathEscape(localFileName)
	}
	return fileURL
}
