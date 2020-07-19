package main

import (
	"encoding/json"
	"log"
	"os"
)

type Configuration struct {
	FFmpeg         string
	Shortname      string
	Username       string
	SlotName       string
	Subdir         string
	Password       string
	PrimaryHost    string
	BackupHost     string
	SourceFile     string
	VideoCodec     string
	VideoFramerate int
	GOPSize        int
	SegmentSize    int
	AudioCodec     string
	CRF            int
	FFmpegLogLevel string
	HLSListSize    int
	Renditions     []Rendition
}

type Rendition struct {
	Name            string
	Height          int
	Width           int
	VideoBitrate    int
	VideoProfile    string
	AudioBitrate    int
	AudioSampleRate int
}

func LoadConfig(configFile string) Configuration {
	file, err := os.Open(configFile)
	if err != nil {
		log.Println("Error reading config file: ", err)
	}
	decoder := json.NewDecoder(file)
	configuration := Configuration{VideoCodec: "h264", VideoFramerate: 30, GOPSize: 30, SegmentSize: 4,
		AudioCodec: "aac", CRF: 30, HLSListSize: 60}
	err = decoder.Decode(&configuration)
	if err != nil {
		log.Println("Error reading config file: ", err)
	}
	return configuration
}
