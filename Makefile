
autobahn-image:
	@docker build . -f autobahn/Dockerfile -t wszero-autobahn --iidfile $@
.PHONY: autobahn-image

autobahn-report: autobahn-image
	@mkdir -p autobahn/report && docker run --rm -it -v ./autobahn/report:/report $$(cat $<)
	@dest=$$PWD/$@; cd autobahn/report && echo *case*.json | xargs -rn1 sh -c 'x=$$(jq -r .behavior <"$$1"); [ $$x = OK -o $$x = INFORMATIONAL ] || echo "$$x $$1"' sh | sort >$$dest
	@cat $@
	@! test -s $@
.PHONY: autobahn-report

gocovmerge:
	go build -o $@ go.shabbyrobe.org/gocovmerge/cmd/gocovmerge

wszero.coverage:
	go test -coverprofile=$@ ./.

wszero_tls.coverage:
	WSZERO_TEST_TLS=1 go test -coverprofile=$@ ./.

all.coverage: gocovmerge wszero.coverage wszero_tls.coverage autobahn/report/autobahn.coverage
	./$^ > $@
