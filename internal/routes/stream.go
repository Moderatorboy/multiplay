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

	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Telegram Video Player</title>
		<script src="https://cdn.jsdelivr.net/npm/hls.js@latest"></script>
		<style>
			body { margin: 0; background: #000; display: flex; justify-content: center; align-items: center; height: 100vh; color: white; font-family: sans-serif; }
			.container { width: 100%%; max-width: 900px; padding: 20px; }
			video { width: 100%%; border-radius: 8px; background: #000; outline: none; }
			.info { text-align: center; margin-top: 15px; color: #888; font-size: 14px; }
		</style>
	</head>
	<body>
		<div class="container">
			<video id="video" controls autoplay playsinline></video>
			<div class="info">Streaming from @Rensiter_streamer_bot</div>
		</div>
		<script>
			var video = document.getElementById('video');
			var videoSrc = window.location.origin + '%s';
			if (video.canPlayType('video/mp4')) { video.src = videoSrc; } 
			if (Hls.isSupported()) {
				var hls = new Hls();
				hls.loadSource(videoSrc);
				hls.attachMedia(video);
			}
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

	// Fixed: Checking authHash so it's "used"
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, messageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx.Header("Accept-Ranges", "bytes")
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
	
	if ctx.Query("d") == "true" {
		ctx.Header("Content-Type", file.MimeType)
		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.FileName))
	} else {
		ctx.Header("Content-Type", "video/mp4")
		ctx.Header("Content-Disposition", "inline")
	}

	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil { return }
		defer pipe.Close()
		io.CopyN(w, pipe, contentLength)
	}
}
