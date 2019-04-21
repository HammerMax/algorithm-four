package main

import (
	"log"
	"net/http"
	"strconv"
)

const port = 2001

func main(){
	go heartbeat.StartHeartbeat()
	go locate.StartLocate()
	http.HandleFunc("/objects/", objects.Handler)
	log.Fatal(http.ListenAndServe(":" + strconv.Itoa(port), nil))
}