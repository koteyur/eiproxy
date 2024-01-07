.PHONY: build
build:
	go run github.com/akavel/rsrc@latest \
		-arch 386 \
		-ico eiproxy.ico \
		-manifest eiproxy.manifest \
		-o rsrc.syso
	GOOS=windows GOARCH=386 go build -ldflags="-H windowsgui"
