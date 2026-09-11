package main

import (
	"log"
	"os"
	"time"
)

func main() {
	log.Print("worker started ", os.Getenv("QUEUE_NAME"))
	for range time.NewTicker(time.Second).C {
		log.Print("tick")
	}
}
