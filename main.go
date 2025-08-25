// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2018-2025 Harald Sitter <sitter@kde.org>

package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/activation"
	"github.com/folbricht/desync"
	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
)

// PrepareReaders can prepare subsequent readers in advance. This notably
// helps offset HTTP latency by issuing the requests ahead of time.
// We wrap all our LazyReaders in PrepareReaders and then activate them by
// doing empty reads on them. This spins up the HTTP request in advance with
// the hope that by the time we actually need to read from them, the request
type PrepareReader struct {
	io.Reader
	Next1 *PrepareReader
	Next2 *PrepareReader
}

func (pr PrepareReader) Read(p []byte) (n int, err error) {
	if len(p) != 0 { // Only prepare if we aren't getting prepared ourself.
		pr.Next1.Prepare()
		pr.Next2.Prepare()
	}

	return pr.Reader.Read(p)
}

func (pr *PrepareReader) Prepare() error {
	_, err := pr.Read([]byte{}) // Empty read to trigger the LazyReader
	return err
}

func file(c *gin.Context) {
	fullpath := c.Param("file")
	path := filepath.Dir(fullpath)
	file := filepath.Base(fullpath)

	// Mind that we are underneath a /kde-linux/ endpoint so our input path is always implicitly prefixed with that.
	url, err := url.Parse("https://files.kde.org/kde-linux/" + path)
	if err != nil {
		panic(err)
	}

	desync.Log.SetOutput(os.Stdout)

	desync.Log.Warn("Requested file:", fullpath)
	desync.Log.Warn("Using URL:", url.String())

	if filepath.Ext(file) != ".erofs" {
		c.Redirect(http.StatusTemporaryRedirect, url.JoinPath(file).String())
		return
	}

	remoteIndexStore, err := desync.NewRemoteHTTPIndexStore(url, desync.StoreOptions{})
	if err != nil {
		panic(err)
	}

	desync.Log.Warn("Using remote index store at", file)
	remoteIndex, err := remoteIndexStore.GetIndex(file + ".caibx")
	if err != nil {
		panic(err)
	}

	erofses, err := filepath.Glob("/system/*.erofs")
	if err != nil {
		panic(err)
	}

	erofsStore, err := desync.NewLocalIndexStore("/system")
	if err != nil {
		panic(err)
	}
	defer erofsStore.Close()

	seeds := []desync.Seed{}
	for _, erofsPath := range erofses {
		erofs := filepath.Base(erofsPath)
		desync.Log.Warn("Using erofs as seed", erofs)

		index, err := erofsStore.GetIndex(erofs + ".caibx")
		if err != nil {
			desync.Log.Warn("Failed to get index for", erofs, ":", err)
			// FIXME hardcoed 32
			index, _, err = desync.IndexFromFile(c, erofsPath, 32, remoteIndex.Index.ChunkSizeMin, remoteIndex.Index.ChunkSizeAvg, remoteIndex.Index.ChunkSizeMax, desync.NewProgressBar("Chunking "))
			if err != nil {
				desync.Log.Warn("Failed to create index for", erofs, ":", err)
				continue
			}
		}

		seed, err := NewIndexSeed("", erofsPath, index)
		if err != nil {
			desync.Log.Warn("Failed to create seed for", erofs, ":", err)
			continue
		}
		seeds = append(seeds, seed)
	}

	assembler, err := stream(c, remoteIndex, NewHTTPSeed(url.JoinPath(file), remoteIndex), seeds, AssembleOptions{})
	if err != nil {
		desync.Log.Error("Failed to create stream:", err)
		panic(err)
	}
	// NOTE: this returns LazyReaders! The lazy readers only open the internal reader when Read() is called!
	readClosers := assembler.Readers()
	defer func() {
		for _, rc := range readClosers {
			rc.Close()
		}
	}()

	prepareReaders := make([]*PrepareReader, len(readClosers))
	for i, rc := range readClosers {
		prepareReaders[i] = &PrepareReader{Reader: rc, Next1: nil, Next2: nil}
	}

	for i, pr := range prepareReaders {
		pr.Next1 = prepareReaders[(i+1)%len(prepareReaders)]
		pr.Next2 = prepareReaders[(i+2)%len(prepareReaders)]
	}

	readers := make([]io.Reader, len(prepareReaders))
	for i, pr := range prepareReaders {
		readers[i] = pr
	}

	c.DataFromReader(http.StatusOK, remoteIndex.Length(), "application/octet-stream",
		io.MultiReader(readers...), map[string]string{})
}

func main() {
	flag.Parse()

	err := sentry.Init(sentry.ClientOptions{
		Dsn: "",
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}
	defer sentry.Flush(2 * time.Second)

	log.Println("Ready to rumble...")
	router := gin.Default(func(e *gin.Engine) {
		e.ContextWithFallback = true
	})
	router.Use(activityTracker())

	router.GET("/kde-linux/*file", file)

	listeners, err := activation.Listeners()
	if err != nil {
		panic(err)
	}

	log.Println("starting servers")
	var servers []*http.Server
	for _, listener := range listeners {
		server := &http.Server{Handler: router}
		go server.Serve(listener)
		servers = append(servers, server)
	}

	if len(servers) == 0 {
		log.Println("servers empty. adding manual server")
		panic("no listeners found, please run with systemd socket activation or use `make run`")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		<-idleTimer.timer.C
		quit <- syscall.SIGQUIT
	}()

	// Wait for some quit cause.
	// This could be INT, TERM, QUIT or the db update trigger.
	// We'll then do a zero downtime shutdown.
	// This relies on systemd managing the socket and us doing graceful listener
	// shutdown. Once we are no longer listening, the system starts backlogging
	// the socket until we get restarted and listen again.
	// Ideally this results in zero dropped connections.
	<-quit
	log.Println("servers are shutting down")

	for _, srv := range servers {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		srv.SetKeepAlivesEnabled(false)
		if err := srv.Shutdown(ctx); err != nil {
			log.Fatalf("Server Shutdown: %s", err)
		}
	}

	log.Println("Server exiting")
}
