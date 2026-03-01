package routes

import (
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	
	r.Engine.GET("/stream/:messageID", getStreamRoute)
	r.Engine.GET("/watch/:messageID", getWatchRoute) 
}

func getWatchRoute(ctx *gin.Context) {
	messageID := ctx.Param("messageID")
	authHash := ctx.Query("hash")
	streamURL := fmt.Sprintf("/stream/%s?hash=%s", messageID, authHash)

	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Telegram Video Player</title>
		<script src="https://cdn.jsdelivr.net/npm/hls.js@latest"></script>
		<style>
			body { margin: 0; background: #000; display: flex; flex-direction: column; justify-content: center; align-items: center; height: 100vh; color: white; font-family: sans-serif; }
			.container { width: 95%%; max-width: 1000px; position: relative; }
			video { width: 100%%; border-radius: 8px; background: #000; box-shadow: 0 0 20px rgba(0,0,0,0.5); }
			.status { margin-top: 10px; font-size: 14px; color: #aaa; }
		</style>
	</head>
	<body>
		<div class="container">
			<video id="video" controls playsinline preload="metadata"></video>
			<div class="status" id="status">Initializing player...</div>
		</div>
		<script>
			var video = document.getElementById('video');
			var status = document.getElementById('status');
			var videoSrc = window.location.origin + '%s';

			function startPlayer() {
				if (Hls.isSupported()) {
					var hls = new Hls({
						enableWorker: true,
						lowLatencyMode: true,
						backBufferLength: 90
					});
					hls.loadSource(videoSrc);
					hls.attachMedia(video);
					hls.on(Hls.Events.MANIFEST_PARSED, function() {
						status.innerText = "Streaming via HLS...";
						video.play();
					});
					hls.on(Hls.Events.ERROR, function(event, data) {
						if (data.fatal) {
							console.log("HLS Error, trying native...");
							video.src = videoSrc;
						}
					});
				} else if (video.canPlayType('application/vnd.apple.mpegurl') || video.canPlayType('video/mp4')) {
					video.src = videoSrc;
					status.innerText = "Streaming via Native Player...";
				}
			}
			startPlayer();
		</script>
	</body>
	</html>`, streamURL)

	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.String(http.StatusOK, html)
}

func getStreamRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, _ := strconv.Atoi(messageIDParm)
	authHash := ctx.Query("hash")

	if authHash == "" {
		http.Error(w, "missing hash", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, messageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Dynamic Mime Handling
	isDownload := ctx.Query("d") == "true"
	mimeType := "video/mp4" // Default for streaming
	disposition := "inline"

	if isDownload {
		mimeType = file.MimeType
		disposition = "attachment"
	}

	// Important Headers for Video Seeking
	ctx.Header("Accept-Ranges", "bytes")
	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Content-Type", mimeType)
	ctx.Header("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", disposition, file.FileName))
	ctx.Header("X-Content-Type-Options", "nosniff")

	var start, end int64
	rangeHeader := r.Header.Get("Range")

	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
		if !isDownload {
			// Video players need 206 Partial Content, not 200 OK hamesha
			w.WriteHeader(http.StatusOK)
		}
	} else {
		ranges, err := range_parser.Parse(file.FileSize, rangeHeader)
		if err != nil {
			// Fallback if range parsing fails
			start = 0
			end = file.FileSize - 1
		} else {
			start = ranges[0].Start
			end = ranges[0].End
			ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
			w.WriteHeader(http.StatusPartialContent)
		}
	}

	contentLength := end - start + 1
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil {
			log.Error("Stream Pipe Error", zap.Error(err))
			return
		}
		defer pipe.Close()
		io.CopyN(w, pipe, contentLength)
	}
}
