package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"
)

type key int

type WorkingState struct {
	at_work   bool
	on_laptop bool
	on_phone  bool
}

type Tags struct {
	Id   string
	Name string
}

type TimeEntryDto struct {
	Id     string
	UserId string
}

const (
	requestIDKey key = 0
)

var (
	listenAddr         string
	healthy            int32
	clockify_key       string
	clockify_workspace string
	clockify_project   string
	state              WorkingState
	lastTimeEntryId    string
	lastUserId         string
	tagMap             map[string]string
)

func main() {

	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	logger.Println("Server is starting...")
	port := os.Getenv("PORT")
	logger.Println(port)

	flag.StringVar(&listenAddr, "listen-addr", ":"+port, "server listen address")
	flag.Parse()

	// Get ENV Variables
	key := os.Getenv("AUTH_KEY")
	clockify_key = os.Getenv("CLOCKIFY_KEY")
	clockify_workspace = os.Getenv("CLOCKIFY_WORKSPACE")
	clockify_project = os.Getenv("CLOCKIFY_PROJECT")

	tagMap = getTags()

	// Init State
	state = WorkingState{
		at_work:   false,
		on_laptop: false,
		on_phone:  false,
	}

	router := http.NewServeMux()
	router.Handle("/", index())
	router.Handle("/health", health())
	router.Handle("/on_phone", on_phone(&state))
	router.Handle("/on_laptop", on_laptop(&state))
	router.Handle("/at_work", at_work(&state))

	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      tracing(nextRequestID)(logging(logger)(auth(key)(router))),
		ErrorLog:     logger,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	go func() {
		<-quit
		logger.Println("Server is shutting down...")
		atomic.StoreInt32(&healthy, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		server.SetKeepAlivesEnabled(false)
		if err := server.Shutdown(ctx); err != nil {
			logger.Fatalf("Could not gracefully shutdown the server: %v\n", err)
		}
		close(done)
	}()

	logger.Println("Server is ready to handle requests at", listenAddr)
	atomic.StoreInt32(&healthy, 1)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("Could not listen on %s: %v\n", listenAddr, err)
	}

	<-done
	logger.Println("Server stopped")
}

func index() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Hello, World!")
	})
}

func health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&healthy) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}
				logger.Println(requestID, r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func auth(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keys, ok := r.URL.Query()["auth"]

			if !ok && len(keys) < 1 || keys[0] != key {
				http.Error(w, "Unauthorized.", 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func at_work(state *WorkingState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateParam, e := checkParamTrue("state", r)

		if e != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		fmt.Println(stateParam)

		if stateParam {
			clock_in("Automated timing", "@Work")
		} else {
			clock_out()
		}
		state.at_work = stateParam
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Succeeded")
	})
}

func on_laptop(state *WorkingState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateParam, e := checkParamTrue("state", r)

		if e != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		state.at_work = stateParam
		clock_in("@Laptop", "")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Succeeded")
	})
}

func on_phone(state *WorkingState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateParam, e := checkParamTrue("state", r)

		if e != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		state.at_work = stateParam
		clock_in("@Phone", "")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Succeeded")
	})
}

func getParamVal(param string, r *http.Request) (string, error) {
	keys, ok := r.URL.Query()[param]

	if !ok && len(keys) < 1 {
		return "", errors.New("State Param not readible.")
	}
	return keys[0], nil
}

func checkParamTrue(param string, r *http.Request) (bool, error) {
	param, err := getParamVal(param, r)
	return param == "true", err
}

func clock_in(message string, tag string) {

	url := "https://api.clockify.me/api/v1/workspaces/" + clockify_workspace + "/time-entries"

	tagString := ""

	if len(tag) > 0 {
		tagString = `"` + tagMap[tag] + `"`
	}

	var jsonStr = []byte(`{
		"start": "` + time.Now().UTC().Format("2006-01-02T15:04:05.000Z") + `",
		"billable": "true",
		"description": "` + message + `",
		"projectId": "` + clockify_project + `",
		"tagIds": [` + tagString + `]
	  }`)

	fmt.Println(string(jsonStr))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("x-api-key", clockify_key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Println("response Status:", resp.Status)
	body, _ := ioutil.ReadAll(resp.Body)

	var response TimeEntryDto
	json.Unmarshal([]byte(string(body)), &response)

	lastTimeEntryId = response.Id
	lastUserId = response.UserId
	fmt.Println("response Body:", string(body))
}

func clock_out() {
	url := "https://api.clockify.me/api/v1/workspaces/" + clockify_workspace + "/user/" + lastUserId + "/time-entries"

	var jsonStr = []byte(`{"end": "` + time.Now().UTC().Format("2006-01-02T15:04:05.000Z") + `"}`)

	fmt.Println(string(jsonStr))
	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("x-api-key", clockify_key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Println("response Status:", resp.Status)
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("response Body:", string(body))

}

func getTags() map[string]string {
	url := "https://api.clockify.me/api/v1/workspaces/" + clockify_workspace + "/tags"

	req, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte("")))
	req.Header.Set("x-api-key", clockify_key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	var tags []Tags
	json.Unmarshal([]byte(string(body)), &tags)

	tagMap := make(map[string]string, 15)
	fmt.Println("Available Tags:")
	for _, tag := range tags {
		fmt.Println(" - " + tag.Name)
		tagMap[tag.Name] = tag.Id
	}
	return tagMap
}
