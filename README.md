# EI Proxy

Tool for setting up public servers in the Evil Islands game without requiring a public IP or VPN.

<p align="center">
  <img src="logo.png" width="150" height="150" alt="EI Proxy">
</p>

## What is it?

Many users typically don't have an option to create a game server that can be accessed externally. They either have to somehow set up a public IP address or use a VPN (for example, Radmin VPN) to play with friends. EI Proxy works on a different principle. It allocates an IP address on an external server, and all traffic goes through it. In this case, only the server-player needs to run the tool, and all external players can connect without the need to install anything.

> [!IMPORTANT]
> The project is under active development. There might be serious bugs and also source code needs
> polishing.

> [!NOTE]
> Code of the server side will be available after beta testing.

## How to build GUI

GUI client itself is only available for Windows. For Linux use wine or the CLI version of the client.

* Windows:
  ```
  cd gui
  go run github.com/akavel/rsrc@latest \
    -arch 386 \
    -ico eiproxy.ico \
    -manifest eiproxy.manifest \
    -o rsrc.syso
  GOARCH=386 go build -ldflags="-H windowsgui"
  ```
* Linux:
  ```bash
  make -C gui
  ```

## License

Licensed under the [MIT No Attribution](LICENSE.txt) license.
