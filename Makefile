
autobahn-image:
	@docker build . -f autobahn/Dockerfile -t wszero-autobahn --iidfile $@
.PHONY: autobahn-image

autobahn-report: autobahn-image
	@mkdir -p autobahn/report && docker run --rm -it -v ./autobahn/report:/report $$(cat $<)
	@dest=$$PWD/$@; cd autobahn/report && echo *case*.json | xargs -rn1 sh -c 'x=$$(jq -r .behavior <"$$1"); [ $$x = OK -o $$x = INFORMATIONAL ] || echo "$$x $$1"' sh | sort >$$dest
	@cat $@
	@! test -s $@
.PHONY: autobahn-report
