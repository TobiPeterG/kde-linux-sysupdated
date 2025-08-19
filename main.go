// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: 2018-2025 Harald Sitter <sitter@kde.org>

package main

import (
	"context"
	"flag"
	"fmt"
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

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	docs "invent.kde.org/kde-linux/sysupdated.git/docs"
)

//go:generate swag init

// sentry-go currently has no support for checkins so we  manually send a heartbeat instead
// https://docs.sentry.io/product/crons/getting-started/http/#heartbeat
func sentryHeartbeat() {
	// Not implemented
}

func sentryTransactor() gin.HandlerFunc {
	return func(c *gin.Context) {
		span := sentry.StartSpan(c, c.Request.Method, sentry.WithTransactionName(c.Request.URL.Path))
		c.Next()
		span.Finish()
	}
}

func main() {
	flag.Parse()

	err := sentry.Init(sentry.ClientOptions{
		Dsn: "",
		// Set TracesSampleRate to 1.0 to capture 100%
		// of transactions for performance monitoring.
		// We recommend adjusting this value in production,
		TracesSampleRate: 0.25,
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}
	defer sentry.Flush(2 * time.Second)

	heartbeat := time.NewTicker(30 * time.Minute)
	go func() {
		for range heartbeat.C {
			sentryHeartbeat()
		}
	}()

	docs.SwaggerInfo.BasePath = "/"

	log.Println("Ready to rumble...")
	router := gin.Default(func(e *gin.Engine) {
		e.ContextWithFallback = true
	})
	// router.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	// router.Use(sentryTransactor())
	router.SetTrustedProxies([]string{"127.0.0.1"})

	router.GET("/kde-linux/*file", func(c *gin.Context) {
		file := filepath.Base(c.Param("file"))

		url, err := url.Parse("https://files.kde.org/kde-linux/")
		if err != nil {
			panic(err)
		}

		desync.Log.SetOutput(os.Stdout)

		desync.Log.Warn("Requested file:", file)
		desync.Log.Warn("Requested file:", c.Param("file"))

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
				index, _, err = desync.IndexFromFile(c, erofsPath, 32, 16*1024, 64*1024, 256*1024, desync.NewProgressBar("Chunking "))
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
		readClosers := assembler.Readers()
		defer func() {
			for _, rc := range readClosers {
				rc.Close()
			}
		}()

		readers := make([]io.Reader, len(readClosers))
		for i, rc := range readClosers {
			readers[i] = rc
		}

		c.DataFromReader(http.StatusOK, remoteIndex.Length(), "application/octet-stream",
			io.MultiReader(readers...), map[string]string{})
	})
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/swagger/index.html")
	})
	router.GET("/doc", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/swagger/index.html")
	})
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	router.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "OK") })

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

		host := os.Getenv("HOST")
		if len(host) <= 0 {
			host = "localhost"
		}
		port := os.Getenv("PORT")
		if len(port) <= 0 {
			port = "3129"
		}

		server := &http.Server{
			Addr:    host + ":" + port,
			Handler: router,
		}
		go server.ListenAndServe()
		servers = append(servers, server)
	}

	addr := servers[0].Addr

	os.MkdirAll("/run/sysupdate.d/50-root-x86-64-erofs.conf.d", 0755)
	os.WriteFile("/run/sysupdate.d/50-root-x86-64-erofs.conf.d/00-default.conf",
		fmt.Appendf(nil, "[Source]\nPath=http://%s/kde-linux\n", addr),
		0644)
	defer os.RemoveAll("/run/sysupdate.d/50-root-x86-64-erofs.conf.d")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

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
