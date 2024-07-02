#!/bin/sh
./autobahn.test -test.coverprofile /report/autobahn.coverage &
pid=$!

rc=0
wstest --mode fuzzingclient --spec /config/fuzzingclient.json || rc=$?
kill -INT $pid
sleep 2
exit $rc
