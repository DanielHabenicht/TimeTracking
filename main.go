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
	logger             *log.Logger
)

func main() {

	logger = log.New(os.Stdout, "http: ", log.LstdFlags)
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
		logger.Println(stateParam)
		state.at_work = stateParam

		evaluateState(state)

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

		logger.Println(stateParam)
		state.on_laptop = stateParam

		evaluateState(state)

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
		logger.Println(stateParam)
		state.on_phone = stateParam

		evaluateState(state)

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Succeeded")
	})
}

func evaluateState(state *WorkingState) {
	logger.Println(*state)
	// type WorkingState struct {at_work, on_laptop, on_phone}
	switch *state {
	case WorkingState{false, false, false}:
		// I am not working so
		clock_out()

	case WorkingState{true, false, false}:
		// I am at work
		clock_in("Normal Work", "@Work")

	case WorkingState{true, true, false}:
		// I am at work, working on my PC
		clock_in("Normal Work", "@PC")

	case WorkingState{true, true, true}:
		// I am at work, working on my PC, taking a call
		clock_in("Normal Work", "@Phone")

	case WorkingState{false, true, false}:
		// I am NOT at work, working on my PC
		clock_in("Remote Work", "@PC")

	case WorkingState{false, true, true}:
		// I am NOT at work, working on my PC, taking a call
		clock_in("Remote Work", "@Phone")

	case WorkingState{false, false, true}:
		// I am NOT at work, NOT ony my PC, taking a call
		clock_in("Remote Work/Call", "@Phone")
	}

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
	logger.Println("Clock in")
	url := "https://api.clockify.me/api/v1/workspaces/" + clockify_workspace + "/time-entries"

	tagString := ""

	if len(tag) > 0 {
		tagString = `"` + tagMap[tag] + `"`
	}

	var jsonStr = `{
		"start": "` + time.Now().UTC().Format("2006-01-02T15:04:05.000Z") + `",
		"billable": "true",
		"description": "` + message + `",
		"projectId": "` + clockify_project + `",
		"tagIds": [` + tagString + `]
	  }`

	var response TimeEntryDto
	request("POST", url, &response, jsonStr)

	logger.Println(string(jsonStr))

	lastTimeEntryId = response.Id
	lastUserId = response.UserId
}

func clock_out() {
	logger.Println("Clock out")
	var jsonStr = `{"end": "` + time.Now().UTC().Format("2006-01-02T15:04:05.000Z") + `"}`
	logger.Println(string(jsonStr))

	url := "https://api.clockify.me/api/v1/workspaces/" + clockify_workspace + "/user/" + lastUserId + "/time-entries"
	var body interface{}
	request("PATCH", url, &body, jsonStr)

	logger.Println("response Body:", body)

}

func getTags() map[string]string {
	var tags []Tags
	request("GET", "https://api.clockify.me/api/v1/workspaces/"+clockify_workspace+"/tags", &tags, "")

	tagMap := make(map[string]string, 15)
	logger.Println("Available Tags:")
	for _, tag := range tags {
		logger.Println(" - " + tag.Name)
		tagMap[tag.Name] = tag.Id
	}
	return tagMap
}

func request(method string, url string, resp interface{}, reqBody string) {
	client := http.Client{
		Timeout: time.Second * 2,
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer([]byte(reqBody)))
	req.Header.Set("x-api-key", clockify_key)
	req.Header.Set("Content-Type", "application/json")

	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("User-Agent", "auto-timetracker")

	res, getErr := client.Do(req)
	if getErr != nil {
		log.Fatal(getErr)
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		log.Fatal(readErr)
	}

	jsonErr := json.Unmarshal(body, &resp)
	if jsonErr != nil {
		log.Fatal(jsonErr)
	}
}
