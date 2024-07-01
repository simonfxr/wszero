#!/bin/sh
./server &

wstest --mode fuzzingclient --spec /config/fuzzingclient.json
