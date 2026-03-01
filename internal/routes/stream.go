package routes

import (
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gotd/td/tg"
	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	r.Engine.GET("/stream/:messageID", getStreamRoute)
    
    // YEH LINE ADD KARO
	r.Engine.GET("/watch/:messageID", getWatchRoute) 
}

func getWatchRoute(ctx *gin.Context) {
	messageID := ctx.Param("messageID")
	authHash := ctx.Query("hash")

	// Streaming link tyaar karna jo usi messageID aur hash ko use karega
	streamURL := fmt.Sprintf("/stream/%s?hash=%s", messageID, authHash)

	// HTML Player Code (Hls.js ke saath)
	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Universal Telegram Player</title>
		<script src="https://cdn.jsdelivr.net/npm/hls.js@latest"></script>
		<style>
			body { margin: 0; background: #0e0e0e; display: flex; justify-content: center; align-items: center; height: 100vh; color: white; font-family: sans-serif; }
			.player-container { width: 95%%; max-width: 1000px; }
			video { width: 100%%; border-radius: 12px; box-shadow: 0 10px 30px rgba(0,0,0,0.5); background: #000; }
			h2 { text-align: center; font-weight: 300; margin-bottom: 20px; color: #bbb; }
		</style>
	</head>
	<body>
		<div class="player-container">
			<h2>Streaming from Telegram...</h2>
			<video id="video" controls autoplay playsinline></video>
		</div>
		<script>
			var video = document.getElementById('video');
			// Hum current domain ka use karke stream URL banate hain
			var videoSrc = window.location.origin + '%s';

			if (Hls.isSupported()) {
				var hls = new Hls({
					enableWorker: true,
					lowLatencyMode: true,
					backBufferLength: 60
				});
				hls.loadSource(videoSrc);
				hls.attachMedia(video);
				hls.on(Hls.Events.MANIFEST_PARSED, function() {
					video.play();
				});
			} else if (video.canPlayType('application/vnd.apple.mpegurl')) {
				video.src = videoSrc;
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
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()

	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	// for photo messages
	if file.FileSize == 0 {
		res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: file.Location,
			Offset:   0,
			Limit:    1024 * 1024,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, ok := res.(*tg.UploadFile)
		if !ok {
			http.Error(w, "unexpected response", http.StatusInternalServerError)
			return
		}
		fileBytes := result.GetBytes()
		ctx.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", file.FileName))
		if r.Method != "HEAD" {
			ctx.Data(http.StatusOK, file.MimeType, fileBytes)
		}
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
		ranges, err := range_parser.Parse(file.FileSize, r.Header.Get("Range"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start = ranges[0].Start
		end = ranges[0].End
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		log.Info("Content-Range", zap.Int64("start", start), zap.Int64("end", end), zap.Int64("fileSize", file.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	}

	contentLength := end - start + 1
	mimeType := file.MimeType

// UNIVERSAL FIX: Agar file extension .mp4 hai par format MPEG-TS hai (Telegram videos)
// toh hum browser ko force karenge ki ise stream ki tarah read kare.
if mimeType == "video/mp4" || mimeType == "" {
    // Hum "video/mp2t" bhejenge kyunki TS files isi par chalti hain
    ctx.Header("Content-Type", "video/mp2t") 
} else {
    ctx.Header("Content-Type", mimeType)
}
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	disposition := "inline"

	if ctx.Query("d") == "true" {
		disposition = "attachment"
	}

	ctx.Header("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", disposition, file.FileName))

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil {
			log.Error("Failed to create stream pipe", zap.Error(err))
			return
		}
		defer pipe.Close()
		if _, err := io.CopyN(w, pipe, contentLength); err != nil {
			if !utils.IsClientDisconnectError(err) {
				log.Error("Error while copying stream", zap.Error(err))
			}
		}
	}
}
