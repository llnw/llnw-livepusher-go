package main

import (
	"fmt"
	log "github.com/llnw/just-go-logging"
	"os/exec"
	"strconv"
	"time"

	"flag"
	"github.com/fsnotify/fsnotify"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

var watcher *fsnotify.Watcher
var ffmpegCmd *exec.Cmd

func main() {
	configPtr := flag.String("config", "conf.json", "Configuration file")
	genPlaybackUrlPtr := flag.Bool("getPlayback", false, "Generate a playback URL from config and exit")
	flag.Parse()
	if configPtr == nil {
		fmt.Println("Couldn't find configuration file argument or the default")
	}
	config := LoadConfig(*configPtr)
	if genPlaybackUrlPtr != nil && *genPlaybackUrlPtr {
		fmt.Println(GetPlaybackUrl(config))
		return
	}

	priBaseUrl := "https://" + config.Username + ":" + config.Password + "@" + config.PrimaryHost + "/push/" +
		config.Shortname + "/" + config.SlotName + "/" + config.Subdir + "/"

	errCh := make(chan error)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM)

	watcher, _ = fsnotify.NewWatcher()
	defer watcher.Close()
	clientMap := make(map[string]*http.Client)

	// Create the master manifest
	manifestName := "chunklist.m3u8"
	masterManifestFile, err := os.Create(manifestName)
	if err != nil {
		log.Fatal(err)
	}
	masterManifestFile.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	// For each rendition:
	//   - add details to master manifest
	//   - create the rendition subdirectory if it does not exist
	//   - Add an fsnotify watcher to the directory
	//   - Create an httpClient and assign it to the rendition's Name in the clientMap
	for _, rendition := range config.Renditions {
		masterManifestFile.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=" +
			strconv.Itoa(rendition.VideoBitrate+rendition.AudioBitrate) +
			",RESOLUTION=" + strconv.Itoa(rendition.Width) + "x" + strconv.Itoa(rendition.Height) + "\n")
		masterManifestFile.WriteString(rendition.Name + "/chunklist" + rendition.Name + ".m3u8\n")
		_ = os.Mkdir("./"+rendition.Name, 0755)
		pwd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		path := pwd + "/" + rendition.Name
		watcher.Add(path)
		httpClient := &http.Client{}
		clientMap[rendition.Name] = httpClient
	}
	masterManifestFile.Sync()
	masterManifestFile.Close()
	go func() {
		PutFile(manifestName, priBaseUrl+manifestName, GetHttpClientFromMap(clientMap))
		os.Remove(manifestName)
	}()

	go func() {
		for {
			select {
			case sig := <-signalChan:
				fmt.Println("Received an interrupt, shutting down. Signal type: " + sig.String())
				ffmpegCmd.Process.Kill()
				os.Exit(0)
			case event := <-watcher.Events:
				switch {
				case event.Op&fsnotify.Write == fsnotify.Write:
					//sometimes we'll get a .tmp file here. Not sure why - just check and break if we do
					if filepath.Ext(event.Name) == ".tmp" {
						break
					}
					dir := filepath.Dir(event.Name)
					parent := filepath.Base(dir)
					location := priBaseUrl + parent + "/" + filepath.Base(event.Name)
					manifest := dir + "/chunklist" + parent + ".m3u8"
					manifestLocation := priBaseUrl + parent + "/chunklist" + parent + ".m3u8"
					//put segment then remove it from local FS, put manifest
					go func() {
						PutFile(event.Name, location, clientMap[parent])
						PutFile(manifest, manifestLocation, clientMap[parent])
						os.Remove(event.Name)
					}()
				}
			case err := <-watcher.Errors:
				errCh <- err
			}
		}
	}()

	StartFFmpeg(config)
	//block here by waiting for an error signal
	log.Fatal(<-errCh)
}

func PutFile(file string, location string, client *http.Client) {
	//fmt.Println("PUT:", file, location)
	data, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
		os.Exit(0)
	}
	defer data.Close()
	req, err := http.NewRequest(http.MethodPut, location, data)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "video/MP2T")
	req.Header.Set("Last-Modified", time.Now().Format(http.TimeFormat))

	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Println("PUT Result: ", res.StatusCode, file, location)
	}
	defer res.Body.Close()
}

func StartFFmpeg(config Configuration) {
	args := []string{}
	if config.FFmpegLogLevel != "" {
		args = append(args, "-v", config.FFmpegLogLevel)
	}
	args = append(args, "-re", "-fflags", "+genpts", "-stream_loop", "-1", "-i", config.SourceFile)
	for _, rendition := range config.Renditions {
		scale := "scale=w=" + strconv.Itoa(rendition.Width) + ":h=" + strconv.Itoa(rendition.Height) + ":force_original_aspect_ratio=decrease"
		args = append(args, "-vf", scale)
		args = append(args, "-c:a", config.AudioCodec)
		args = append(args, "-ar", strconv.Itoa(rendition.AudioSampleRate))
		args = append(args, "-b:a", strconv.Itoa(rendition.AudioBitrate))
		args = append(args, "-c:v", config.VideoCodec)
		args = append(args, "-b:v", strconv.Itoa(rendition.VideoBitrate))
		args = append(args, "-maxrate", strconv.Itoa(int(float64(rendition.VideoBitrate)*1.07)))
		args = append(args, "-bufsize", strconv.Itoa(int(float64(rendition.VideoBitrate)*1.5)))
		args = append(args, "-profile:v", rendition.VideoProfile)
		args = append(args, "-crf", strconv.Itoa(config.CRF))
		args = append(args, "-sc_threshold", "0")
		args = append(args, "-g", strconv.Itoa(config.GOPSize))
		args = append(args, "-keyint_min", strconv.Itoa(config.GOPSize))
		args = append(args, "-f", "hls")
		args = append(args, "-hls_flags", "program_date_time+omit_endlist")
		args = append(args, "-hls_time", strconv.Itoa(config.SegmentSize))
		args = append(args, "-hls_list_size", strconv.Itoa(config.HLSListSize))
		segmentFilename := "./" + rendition.Name + "/chunk%d.ts"
		manifestFilename := "./" + rendition.Name + "/chunklist" + rendition.Name + ".m3u8"
		args = append(args, "-hls_segment_filename", segmentFilename, manifestFilename)
	}
	ffmpegCmd = exec.Command(config.FFmpeg, args[0:]...)
	ffmpegCmd.Stdout = os.Stdout
	ffmpegCmd.Stderr = os.Stderr
	err := ffmpegCmd.Run()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
	}
}

func GetHttpClientFromMap(m map[string]*http.Client) *http.Client {
	for k := range m {
		return m[k]
	}
	return nil
}

func GetPlaybackUrl(config Configuration) string {
	return "https://" + config.Shortname + "-livepush.video.llnw.net/" + config.SlotName + "/" +
		config.Subdir + "/chunklist.m3u8"
}
