# Running

Magooify is software with a web-based user interface, it runs as a small, self-contained web server that you can point a web browser at. Although it runs as a "server", it doesn't need a huge, dedicated server machine to run on, it will run very happily on most laptops or desktops.

## Desktop

Simply download and run the executable for your platform from the [project homepage](https://www.sansay.co.uk/magooify/). You can run Magooify by simply double-clicking and running the executable you downloaded. You should see the server component running in a terminal window with a few lines of information, and your default web browser should open and show the user interface.

To be able to process images with AI, you will need to run Magooify from the command-line and supply an OpenRouter API key as a command-line option. Open a terminal / command prompt window (this should work on Windows, MacOS and Linux) and type:

```
magooify -openrouter-key=sk-or-v1-..
```

Without `-openrouter-key` the application will hide the prompt editor and the "Models" picker so the captured image is never sent to be processed by an AI service, keeping your content entirely on your own system - in this case, it shouldn't even need internet access.

By default, processed images will be placed in a folder called "Magooify" in the same location as the application is run from. You can change this by specifying the output folder with the `-output-dir` option at the command line:

```
magooify -openrouter-key=sk-or-v1-.. -output-dir=/home/myusername/mypictures
```

If you want to use Magooify regularly in desktop mode you should probably create a simple script / batch file to start it up with the OpenRouter key you want to use.

To do: for end-user convenience, package the executable in a Windows / MacOS installer that asks for an optional API key and creates a shortcut to run the application in desktop mode.

## Server

If you are a system administrator of some kind, wanting multiple of your users able to use Magooify, then it is designed to sit behind an authenticating proxy server such as Cloudflare Tunnels, Pangolin, or similar. Magooify will expect to be passed the authenticated user's username via HTTP header, which should be default behaviour for most such systems.

A common setup with such systems is for applications to be run in their own (Docker, Podman, etc) container. Magooify is written in Go, with the executable compiled with CGO disabled (CGO_ENABLED=0), therefore it should work in a very minimal container image, even the completely empty `scratch` image.

In testing and development, Magooify ran on a basic virtual machine on a shared cloud provider with 2GB of RAM and 80GB of disk space - and that's including the whole desktop Linux development environment, an instance of Pangolin and the OpenCode development tool.

For instance, if you were using Pangolin as your authenticating server, you would add a basic Dockerfile:

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

## Optional flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-openrouter-key` | (unset) | OpenRouter API key used to process captured images. Without it the prompt editor and Models picker are hidden in the UI; the captured image can still be vectorised locally or saved as a bitmap scan, but is never sent to OpenRouter. |
| `-openrouter-management-key` | (unset) | OpenRouter management key used to query the account balance. Optional; without it the UI shows only the session's spend. Cannot be used to process images. |
| `-output-dir` | `magooify` | Folder where processed images are stored. |
| `-model` | `google/gemini-3.1-flash-lite-image` | OpenRouter model used to process the images (any image-capable model). |
| `-prompt-file` | (unset) | Path to a file whose text is sent to the model with each image, instead of the embedded `PROMPT.md`. |
| `-port` | `8080` | Port for the local server. |
| `-mode` | `desktop` | `desktop` or `server`. |
| `-host` | (derived) | Host IP to bind to (`127.0.0.1` in desktop mode, `0.0.0.0` in server mode). |
| `-base-path` | (unset) | Reverse-proxy sub-path prefix, e.g. `/magooify`. |
| `-no-browser` | `false` | Do not auto-launch the browser in desktop mode. |

The environment variables `OPENROUTER_API_KEY`, `OPENROUTER_MANAGEMENT_KEY`, `OUTPUT_DIR`, `OPENROUTER_MODEL`, `PROMPT_FILE`, `PORT`, `APP_MODE`, `HOST` and `BASE_PATH` can be used instead of the equivalent flags.

### Customising the Processing Instructions

The instructions sent to the AI model alongside each image come from the [`PROMPT.md`](PROMPT.md) file at the repository root. The file's text is embedded into the executable at build time, so you can change what the model does with each image simply by editing that file and rebuilding. If `PROMPT.md` is empty or missing the application falls back to a built-in default prompt.

To use a different prompt without rebuilding, point the `-prompt-file` option at your own text file:

```
./magooify -openrouter-key=sk-or-v1-... -prompt-file=~/my-instructions.txt
```

The file's text is read from disk on each use; if it can't be read or is empty, the embedded `PROMPT.md` text (or the built-in default) is used instead.

### Tracking Spend

The UI shows the total cost of the current session (in US dollars, formatted to your locale) in the top bar. When you also supply an OpenRouter *management* key with `-openrouter-management-key` (or the `OPENROUTER_MANAGEMENT_KEY` environment variable), the remaining account balance is shown alongside it. Management keys are administrative-only: they can query your credits but cannot process images, so both keys are needed to see the balance. If the management key is missing or the query fails, the balance is simply hidden and only the session cost is shown.

### Choosing a Model

The **Models** button in the top bar opens a searchable list of the OpenRouter models that can process an image and return a processed image (fetched live from OpenRouter's `/api/v1/images/models` endpoint, which requires your OpenRouter API key, and cached briefly), together with the cost of processing a single image with each one. Click any row to make that model active immediately; the currently configured model is marked *active*. The images listing covers every image model, including image-to-image models such as the Recraft family that the general models endpoint omits. When OpenRouter publishes an exact per-image price for a model it is used; otherwise the cost is estimated from the model's published per-token rates assuming a typical 1024x1024 image plus a generated image output. Prices change, so treat the figures as guidance and check OpenRouter before committing to a model.

### Vectorising Results to SVG

Tick the **Convert result to SVG** box above the Process button and the processed image is traced into a resolution-independent SVG document before being saved, so it can be scaled to any size without pixelation or saved for use in vector drawing tools. The trace is done entirely inside the executable by an embedded vectoriser (in `main.go`) using only the Go standard library: the artwork is split into colour layers matching the bitmap version, each layer's pixels are followed around their boundaries, genuine corners are detected, and each stretch of boundary is fitted with smooth cubic bezier curves. Up to 16 colours are kept as their own layers (each filled with the average colour of its pixels), every other colour is merged into the nearest one, and each layer is emitted as a group with the even-odd fill rule so holes and enclosed shapes trace correctly. Images the model already returns as SVG (some models can be asked directly for vector output) are saved as-is. If the model's output is a format the tracer cannot decode, the request is rejected with a clear error rather than silently saved untraced.

When no OpenRouter API key is configured the UI hides the AI-only controls (prompt editor, Models picker, credit balance) but the **Convert result to SVG** switch stays interactive, so every captured image can be either saved as a bitmap scan or traced into an SVG locally. The captured image is never sent off the machine in this mode - both code paths run entirely inside the executable without contacting OpenRouter.
