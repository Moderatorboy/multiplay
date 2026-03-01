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
		<script src="https://cdn.jsdelivr.net/npm/plyr@3.7.8/dist/plyr.polyfilled.js"></script>
		<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/plyr@3.7.8/dist/plyr.css" />
		<style>
			body { margin: 0; background: #000; height: 100vh; display: flex; align-items: center; justify-content: center; }
			.wrapper { width: 100%%; max-width: 950px; }
			video { width: 100%%; border-radius: 10px; }
		</style>
	</head>
	<body>
		<div class="wrapper">
			<video id="player" playsinline controls preload="metadata">
				<source src="%s" type="video/mp4" />
			</video>
		</div>
		<script>
			const player = new Plyr('#player', {
				autoplay: true,
				invertTime: false,
				toggleInvert: false,
			});
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
		ctx.AbortWithStatus(http.StatusForbidden)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, messageID)
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	isDownload := ctx.Query("d") == "true"
	
	// FIX FOR MPEG-TS: Agar file TS format mein hai toh sahi mime bhejenge
	mimeType := "video/mp4"
	if strings.HasSuffix(strings.ToLower(file.FileName), ".ts") || file.MimeType == "video/mp2t" {
		mimeType = "video/mp2t"
	}

	ctx.Header("Accept-Ranges", "bytes")
	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("X-Content-Type-Options", "nosniff")

	if isDownload {
		ctx.Header("Content-Type", "application/octet-stream")
		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.FileName))
	} else {
		ctx.Header("Content-Type", mimeType)
		ctx.Header("Content-Disposition", "inline")
	}

	var start, end int64
	rangeHeader := r.Header.Get("Range")

	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
		w.WriteHeader(http.StatusOK)
	} else {
		ranges, _ := range_parser.Parse(file.FileSize, rangeHeader)
		if len(ranges) > 0 {
			start = ranges[0].Start
			end = ranges[0].End
			ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			start = 0
			end = file.FileSize - 1
		}
	}

	contentLength := end - start + 1
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil { return }
		defer pipe.Close()
		io.CopyN(w, pipe, contentLength)
	}
}
