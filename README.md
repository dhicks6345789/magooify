# <img src="./ui/logo.svg" width="50" valign="middle"> Magooify

Turn your quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours.

## Features

- **Image Capture**: Take photos from your device (laptop, desktop, phone or tablet), scan from a [desktop visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser), or simply upload an image.
- **Image Processing**: Images can (optionally) be processed by AI to smooth lines, create consistent colours and fill shapes. The prompt used for processing is customisable, and the AI model used can be selected, with costs displayed.
- **Vectorise**: Optionally trace the processed image into a scaleable SVG image.
- **Designed For Schools**: Can integrate with your authentication provider (Google, Microsoft, etc) via an authenticating proxy (Cloudflare Tunnels, Pangolin, etc), cloud storage (Google Drive, OneDrive, etc) and filtered / budget-controlled AI API router, making it suitable for educational establishment looking to manage their pupil's AI tool usage. It can even match the colours of the felt-tip pens, pencils or crayons you use in class!
- **Self-Hostable**: With a web-based interface and a self-contained, single-executable backend, this application is self-hostable on pretty much any platform, meaning you can control exactly what happens to your user's content.
- **Easy To Customise**: Built in [Go](https://developers.googleblog.com/why-go-is-an-ideal-language-for-ai-assisted-software-engineering), and designed for development using an AI agent, adding or changing features should be a simple case of describing what you want done.

---

## How To Use

This application is designed to process quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours. It's a workflow tool designed with beginners in mind, capable of bulk processing images, and can write processed images to a shared cloud storage location - perfect for school art classes.

The first thing you will see is an option to either take a photo or upload an image. If you are on a phone or tablet you can simply take a photo of the cartoon you want to use. On a laptop or desktop machine, a [desktop USB visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser) is probably your best option.

Next, select if you want to palette-lock to a known set of colours. A wide range of palettes from pen, pencil and crayon sets used in UK schools are included. Selecting a known colour palette will enable processing of your image to select consistent, smooth colours for coloured areas.

Select the "Vectorise" option if you want your output to be an SVG image, scaleable to fit any size and resolution of media - use your artwork in a book, on a t-shirt or a 96-sheet roadside billboard!

## Running

You can use Magooify right away by simply downloading and running the executable for your platform from the project homepage. Simply run the executable, in desktop mode it should open your local web browser and display the user interface.

To use the AI image processing feature, you will need to provide an OpenRouter API key.

For more details, or if you are a system administrator wanting to run this application centrally for access by multiple users, see the [documentation](docs/running.md).

## Developers

If you want to use, customise or extend this project - for use by yourself, other users in your school / workplace, or even as a service you charge money for - you are very welcome to. See the [developer's documentation](docs/developers.md) for more details.

This project is distributed under a permissive [Apache 2.0 license](LICENSE) - you are allowed to freely use, modify, and distribute this project for both personal and commercial purposes without paying royalties.
