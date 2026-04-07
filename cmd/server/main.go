package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	fmt.Println("Starting Ratatosk Server (Relay)...")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Ratatosk Relay Server is running")
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}
