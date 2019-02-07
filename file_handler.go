package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/nlopes/slack"
)

// FileHandler downloads files from slack
type FileHandler struct {
	SlackAPIKey          string
	FileDownloadLocation string
	ProxyPrefix          string
}

// Download downloads url contents to a local file
func (handler *FileHandler) Download(file slack.File) string {
	fileURL := file.URLPrivate
	if handler.FileDownloadLocation == "" {
		return fileURL
	}
	if handler.SlackAPIKey == "" {
		return fileURL
	}
	if file.IsExternal {
		return fileURL
	}
	localFileName := fmt.Sprintf("%s_%s.%s", file.ID, file.Title, file.Filetype)
	localFilePath := filepath.Join(handler.FileDownloadLocation, localFileName)
	go func() {
		out, err := os.Create(localFilePath)
		if err != nil {
			log.Printf("Could not create file for download %s", localFilePath)
			return
		}

		defer out.Close()
		request, _ := http.NewRequest("GET", fileURL, nil)
		request.Header.Add("Authorization", "Bearer "+handler.SlackAPIKey)
		var client = &http.Client{}
		resp, err := client.Do(request)
		if err != nil {
			log.Printf("Error downloading %v", file)
			return
		}
		if resp.StatusCode != http.StatusOK {
			log.Printf("Got %d while downloading %s", resp.StatusCode, fileURL)
			return
		}
		defer resp.Body.Close()
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			log.Printf("Error writing %s", fileURL)
		}
		return
	}()
	if handler.ProxyPrefix != "" {
		return handler.ProxyPrefix + url.PathEscape(localFileName)
	}
	return fileURL
}
