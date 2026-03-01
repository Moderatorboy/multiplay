package routes

import (
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"io"
	"net/http"
	"strconv"

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

	// Ultra-Compatible Video Player
	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Telegram Player - Playing...</title>
		<script src="https://cdn.jsdelivr.net/npm/hls.js@latest"></script>
		<style>
			body { margin: 0; background: #000; display: flex; justify-content: center; align-items: center; height: 100vh; color: white; font-family: sans-serif; overflow: hidden; }
			.container { width: 100%%; max-width: 1000px; }
			video { width: 100%%; max-height: 90vh; border-radius: 12px; box-shadow: 0 0 50px rgba(0,0,0,1); background: #000; }
			.info { text-align: center; margin-top: 10px; color: #555; font-size: 12px; letter-spacing: 1px; }
		</style>
	</head>
	<body>
		<div class="container">
			<video id="video" controls autoplay playsinline preload="auto"></video>
			<div class="info">POWERED BY RENSITER STREAMER</div>
		</div>
		<script>
			var video = document.getElementById('video');
			var videoSrc = window.location.origin + '%s';

			if (Hls.isSupported()) {
				var hls = new Hls();
				hls.loadSource(videoSrc);
				hls.attachMedia(video);
			} else if (video.canPlayType('application/vnd.apple.mpegurl') || video.canPlayType('video/mp4')) {
				video.src = videoSrc;
			}
			video.play().catch(e => console.log("Autoplay blocked, waiting for interaction"));
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
		ctx.AbortWithStatus(http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, messageID)
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Range & Security Headers
	ctx.Header("Accept-Ranges", "bytes")
	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	
	var start, end int64
	rangeHeader := r.Header.Get("Range")

	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
		w.WriteHeader(http.StatusOK)
	} else {
		ranges, _ := range_parser.Parse(file.FileSize, rangeHeader)
		start = ranges[0].Start
		end = ranges[0].End
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	}

	contentLength := end - start + 1
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	// DISPOSITION LOGIC - Yahan hai asli magic
	if ctx.Query("d") == "true" {
		ctx.Header("Content-Type", "application/octet-stream")
		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.FileName))
	} else {
		// Browser ko force karenge streaming mode mein
		ctx.Header("Content-Type", "video/mp4") 
		ctx.Header("Content-Disposition", "inline")
		ctx.Header("X-Content-Type-Options", "nosniff") // Browser ki "automatic download" aadat rokne ke liye
	}

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil { return }
		defer pipe.Close()
		io.CopyN(w, pipe, contentLength)
	}
}
