# Magooify

Magooify is a self-hostable Go application that bundles application logic, an embedded Bootstrap web user interface and interactive OpenAPI documentation into a single executable.

The web interface lets you take a photo with your device camera or upload an image file. Magooify sends the image to a vision-capable model on OpenRouter for processing, then saves the image and the model's description to a folder on disk.

## Features

- **Image Capture**: Take photos directly with your device camera (via `getUserMedia`) or upload image files from your machine.
- **OpenRouter Processing**: Captured images are sent to a vision model on OpenRouter for description and analysis.
- **File System Storage**: Processed images and their text descriptions are saved to a configurable output directory.
- **Single Executable Deployment**: Uses Go's `embed` package to bundle the frontend HTML / CSS / JavaScript assets, documentation and OpenAPI specifications into a single binary.
- **Offline Operation**: Designed to be able to operate without internet access; all UI libraries and documentation resources are served locally. (Note: processing an image needs internet access to reach OpenRouter.)
- **Works on Desktop and Server**:
  - **Desktop Mode**: Ideal for local home desktop use on pretty much any platform ([Windows](https://www.microsoft.com/windows/) / [Windows Server](https://www.microsoft.com/windows-server), [MacOS](https://en.wikipedia.org/wiki/MacOS), and [Linux](https://en.wikipedia.org/wiki/Linux), including both the [Raspberry Pi](https://www.raspberrypi.com/) (and other single-board computers) and [ChromeOS](https://chromeos.google/intl/en_uk/products/chromeos-flex/) ([Crostini](https://chromeos.dev/en/linux))). Running the executable on your desktop machine should give you a localhost-only server and automatically launch your default web browser to display the user interface.
  - **Server Mode**: Suitable for multi-user deployment behind authenticating reverse proxies ([Pangolin](https://pangolin.net/), [Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/), [Authelia](https://www.authelia.com/), [Tailscale](https://tailscale.com/), [etc](https://github.com/anderspitman/awesome-tunneling)). Authenticates users via incoming proxy headers (`X-Forwarded-User`, `Remote-User`, `Pangolin-User`, etc.). As a single, statically linked Go binary with no external dependencies, it can be run inside a very minimal (`scratch`) container environment.
- **Built for use by humans and AI agents**: With built-in documentation and Swagger UI, documentation should be easy to understand for both humans and AI agents.
- **Built for extending by humans and AI agents**: Point an AI coding agent at AGENTS.md (or read it yourself) and use this project as a basis for your own app, the project structure is kept deliberately simple.

---

## Running

### Desktop

Simply download and run the executable for your platform from the [project homepage](https://sansay.co.uk/magooify/). To process images you need to supply an OpenRouter API key and the folder where processed images should be stored:

```
./magooify -openrouter-key=sk-or-v1-... -output-dir=~/magooify-images
```

Optional flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-openrouter-key` | (unset) | OpenRouter API key used to process captured images. Without it the image endpoint reports that OpenRouter is not configured. |
| `-output-dir` | `processed` | Folder where processed images and their text descriptions are stored. |
| `-model` | `google/gemini-3.1-flash-lite-image` | OpenRouter model used to process the images (any vision-capable model). |
| `-port` | `8080` | Port for the local server. |
| `-mode` | `desktop` | `desktop` or `server`. |
| `-host` | (derived) | Host IP to bind to (`127.0.0.1` in desktop mode, `0.0.0.0` in server mode). |
| `-base-path` | (unset) | Reverse-proxy sub-path prefix, e.g. `/magooify`. |
| `-no-browser` | `false` | Do not auto-launch the browser in desktop mode. |

The environment variables `OPENROUTER_API_KEY`, `OUTPUT_DIR`, `OPENROUTER_MODEL`, `PORT`, `APP_MODE`, `HOST` and `BASE_PATH` can be used instead of the equivalent flags.

### Server

You can run Magooify behind an authenticating proxy server - the proxy server handles HTTPS, authenticates the user and passes the username to the application via an HTTP header. As the executable is compiled with CGO disabled (CGO_ENABLED=0) and the proxy server is dealing with HTTPS, the container environment can use the `scratch` (completely empty) container image. For instance, if you were using Pangolin as your authenticating server, you would add a basic Dockerfile:

```
# Note: the "scratch" image is 0 bytes, it doesn't have tools like chmod, so there's some extra steps
# needed to get executable files inside a "scratch" image. We need to build a "downloader" image...
FROM alpine:latest AS downloader
RUN apk add --no-cache curl && \
    curl -L https://www.sansay.co.uk/magooify/magooify-linux-amd64 -o /magooify && \
    chmod +x /magooify

# ...then use that to build the actual image we want.
FROM scratch
COPY --from=downloader /magooify /magooify
```
And to docker compose, add something like:
```
magooify:
    image: MAGOOIFY_IMAGE
    command: /magooify -mode=server -port=8080 -base-path=/magooify
    container_name: magooify
    restart: unless-stopped
```
From the Pangolin control panel you would then create a resource, possibly with a prefix ("magooify" or whatever you have named your derived app) using whatever authentication and access controls you like, that pointed at that container (`magooify:8080`). If you do use a prefix for the resource, be sure to add that prefix as the "-base-path" option when running the server.

## Building

Clone the repository:

```
git clone https://github.com/dhicks6345789/magooify.git
```

And run build:

```
cd magooify
bash build.sh build-all
```

This will compile the executables for all platforms.

You can build executables and generate documentation, including Swaggo's interactive API documentation, and copy the lot directly to somewhere they can be served as a web site to act as a project homepage using "build.sh dist". You just need to specify the path you want the files to go to, e.g.:

```
bash build.sh dist ~/www/magooify
```

## Using As a Basis For Your Own Projects

The purpose of this project is to act as a basic starting point for a self-contained "app" that is easy for end users to run and use. It should produce executables able to run on your preferred platform, either as a "desktop app" (a local-only server with a web interface) or as a compact server. Compiling and running the project gives you an image capture and processing application that calls OpenRouter and stores the results on disk, useful to test your build process and that any authentication / endpoint routing is working okay.

Extending this project should be a case of cloning the Git repository and adding your own functions to `api.go` and user interface elements to `ui/index.html`, either by hand or by instructing an AI coding agent. An AGENTS.md is included.

This project was built using OpenCode and its "Big Pickle" model (free, as of August 2026), an instance of GLM-4.6. Documentation (this README) was written by hand.
