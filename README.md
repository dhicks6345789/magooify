# <img src="./ui/logo.svg" width="50" valign="middle"> Magooify

Turn your quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours.

## Features

- **Image Capture**: Take photos from your device (laptop, desktop, phone or tablet), scan from a [desktop visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser), or simply upload an image.
- **AI Image Processing**: Images are processed to smooth lines, create consistent colours and fill shapes. The prompt used for processing is customisable, and the AI model used can be selected.
- **Vectorise to SVG**: Optionally trace the processed image into a scaleable SVG image.
- **Designed For Schools**: Can integrate with your authentication provider (Google, Microsoft, etc) via an authenticating proxy (Cloudflare Tunnels, Pangolin, etc), cloud storage (Google Drive, OneDrive, etc) and filtered / budget-controlled AI API router, making it suitable for educational establishment looking to manage their pupil's AI tool usage. It can even colour-match the felt-tip pens, pencils or crayons you use in class!
- **Self-Hostable**: With a web-based interface and a self-contained, single-executable backend, this application is self-hostable on pretty much any platform, meaning you can control exactly what happens to your user's content.
- **Easy To Customise**: Built in [Go](https://developers.googleblog.com/why-go-is-an-ideal-language-for-ai-assisted-software-engineering) using an AI agent, adding or changing features should be a simple case of describing what you want done.

---

## Using

This application is designed to process quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours.

The first thing you will see is an option to either take a photo or upload an image. If you are on a phone or tablet you can simply take a photo of the cartoon you want to use. On a laptop or desktop machine, a [desktop USB visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser) is probably your best option


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
