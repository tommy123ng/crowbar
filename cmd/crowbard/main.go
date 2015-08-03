// Copyright (c) 2015, Segiusz 'q3k' Bazanski <sergiusz@bazanski.pl>
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
// 1. Redistributions of source code must retain the above copyright notice,
//    this list of conditions and the following disclaimer.
// 2. Redistributions in binary form must reproduce the above copyright
//    notice, this list of conditions and the following disclaimer in the
//    documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
// SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
// CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
// ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.

package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"os"
	"net/http"
	"strconv"

	"github.com/pborman/uuid"

	"github.com/q3k/crowbar"
)

var nonceMap = map[string][]byte{}

func authHandler(w http.ResponseWriter, r *http.Request) {
	nonce := make([]byte, 16)
	_, err := rand.Read(nonce)
	if err != nil {
		crowbar.WriteHTTPError(w, "Internal error.")
		return
	}

	username := r.URL.Query().Get("username")
	_, ok := UserGet(username)

	if !ok {
		crowbar.WriteHTTPError(w, "No such user.")
		return
	}

	nonceMap[username] = nonce
	crowbar.WriteHTTPData(w, nonce)
}

func connectHandler(w http.ResponseWriter, r *http.Request) {
	remote_host := r.URL.Query().Get("remote_host")
	if remote_host == "" {
		crowbar.WriteHTTPError(w, "Invalid host")
		return
	}
	remote_port, err := strconv.Atoi(r.URL.Query().Get("remote_port"))
	if err != nil || remote_port > 0xFFFF {
		crowbar.WriteHTTPError(w, "Invalid port number.")
		return
	}
	username := r.URL.Query().Get("username")
	if username == "" {
		crowbar.WriteHTTPError(w, "Invalid username")
		return
	}
	user, ok := UserGet(username)
	if !ok {
		crowbar.WriteHTTPError(w, "Invalid username")
		return
	}
	nonce, ok := nonceMap[username]
	if !ok {
		crowbar.WriteHTTPError(w, "Invalid username")
		return
	}

	proof_b64 := r.URL.Query().Get("proof")
	decodeLen := base64.StdEncoding.DecodedLen(len(proof_b64))
	proof := make([]byte, decodeLen)
	n, err := base64.StdEncoding.Decode(proof, []byte(proof_b64))
	if err != nil {
		crowbar.WriteHTTPError(w, "Invalid nonce")
		return
	}
	proof = proof[:n]

	authenticated := user.Authenticate(nonce, proof)
	if !authenticated {
		crowbar.WriteHTTPError(w, "Invalid nonce")
		return
	}

	workerUuid := uuid.New()
	commandChannel := make(chan workerCommand, 10)
	responseChannel := make(chan workerResponse, 10)
	fmt.Printf("Connecting to %s:%d...\n", remote_host, remote_port)
	remote, err := net.Dial("tcp", fmt.Sprintf("%s:%d", remote_host, remote_port))
	if err != nil {
		crowbar.WriteHTTPError(w, fmt.Sprintf("Could not connect to %s:%d", remote_host, remote_port))
		return
	}

	newWorker := worker{remote: remote, commandChannel: commandChannel, responseChannel: responseChannel, uuid: workerUuid}
	workerMap[workerUuid] = newWorker

	crowbar.WriteHTTPOK(w, workerUuid)

	go socketWorker(newWorker)
}

func syncHandler(w http.ResponseWriter, r *http.Request) {
	workerUuid := r.URL.Query().Get("uuid")
	if worker, ok := workerMap[workerUuid]; ok {
		if r.Method == "POST" {
			r.ParseForm()
			if b64_parts, ok := r.Form["data"]; ok {
				b64 := b64_parts[0]
				decodeLen := base64.StdEncoding.DecodedLen(len(b64))
				data := make([]byte, decodeLen)
				n, err := base64.StdEncoding.Decode(data, []byte(b64))
				if err != nil {
					crowbar.WriteHTTPError(w, "Could not decode B64.")
				} else {
					worker.commandChannel <- workerCommand{command: command_data, extra: data[:n]}
					crowbar.WriteHTTPOK(w, "Sent.")
				}
			} else {
				crowbar.WriteHTTPError(w, "Data is required.")
			}
		} else {
			response := <-worker.responseChannel
			switch response.response {
			case response_data:
				crowbar.WriteHTTPData(w, response.extra_byte)
			case response_quit:
				crowbar.WriteHTTPQuit(w, response.extra_string)
			}
		}
	} else {
		crowbar.WriteHTTPError(w, "No such UUID")
	}
}

func main() {
	var listen = flag.String("listen", "0.0.0.0:8080", "Address to bind HTTP server to")
	var userfile = flag.String("userfile", "/etc/crowbard.conf", "Path of user config file")
	flag.Parse()
	loadUsersFromFile(*userfile)
	fmt.Fprintf(os.Stderr, "Server starting on %s...\n", *listen)
	http.HandleFunc(crowbar.EndpointConnect, connectHandler)
	http.HandleFunc(crowbar.EndpointSync, syncHandler)
	http.HandleFunc(crowbar.EndpointAuth, authHandler)
	http.ListenAndServe(*listen, nil)
}
