# <img src="./ui/logo.svg" width="50" valign="middle"> Magooify

Turn your quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours.

You can try a [live demo](https://www.sansay.co.uk/magooify/demo/) right now.

## Features

- **Image Capture**: Take photos from your device (laptop, desktop, phone or tablet), scan from a [desktop visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser), or simply upload an image.
- **Image Processing**: Images can (optionally) be processed by AI to smooth lines, create consistent colours and fill shapes. The prompt used for processing is customisable, and the AI model used can be selected, with costs displayed.
- **Vectorise**: Optionally trace any captured image into a scaleable SVG image. This step runs entirely inside the executable and does not require an OpenRouter API key, so the app is still useful as a local vectoriser when AI processing is disabled.
- **Designed For Schools**: Can integrate with your authentication provider (Google, Microsoft, etc) via an authenticating proxy (Cloudflare Tunnels, Pangolin, etc), cloud storage (Google Drive, OneDrive, etc) and filtered / budget-controlled AI API router, making it suitable for educational establishment looking to manage their pupil's AI tool usage. It can even match the colours of the felt-tip pens, pencils or crayons you use in class!
- **Self-Hostable**: With a web-based interface and a self-contained, single-executable backend, this application is self-hostable on pretty much any platform, meaning you can control exactly what happens to your user's content.
- **Easy To Customise**: Built in [Go](https://developers.googleblog.com/why-go-is-an-ideal-language-for-ai-assisted-software-engineering), and designed for development using an AI agent, adding or changing features should be a simple case of describing what you want done.

---

## How To Use

This application is designed to process quick, roughly-sketched pen-and-ink black-line style cartoons into clean, scaleable images with smooth lines and consistent colours. It's a workflow tool designed with beginners in mind, capable of bulk processing images, and can write processed images to a shared cloud storage location - perfect for busy school art classes.

### Draw

Ideally, hand-draw your cartoon on clean, white paper. Use black lines to define shapes and boundaries. If you sketch lines in pencil first you might want to go over in black ink to improve contrast before you scan the image. Colour using felt-tip pen, highlighter pen, colouring pencil or crayon. Fill shapes as well as you can, but you don't need to be an expert - if you are a teacher (or parent), the kind of artwork the average 5-to-10 year old produces is probably what we're aiming for here.

### Capture (or Upload) Image

At thge top of the app's page is an option to either take a photo or upload an image. If you are on a phone or tablet you can simply take a photo of your cartoon. On a laptop or desktop machine, a [desktop USB visualiser](https://www.amazon.co.uk/s?k=desktop+visualiser) is probably your best option. If you have a desktop flatbed scanner, you can upload image files from that.

### Lock Colours

Next, select if you want to palette-lock to a known set of colours. A wide range of palettes from pen, pencil and crayon sets used in UK schools are included. Any colours in the image, which for a scan or a photo will include subtle shades caused by lighting and natural variation, will be locked to their nearest, single, well-defined colour as given in the pen, pencil or crayon set you choose.

### Tracking AI Spend

The application shows the total cost of the current session in the top bar. When you also supply an OpenRouter *management* key with `-openrouter-management-key` (or the `OPENROUTER_MANAGEMENT_KEY` environment variable), the remaining account balance is shown alongside it. Management keys are administrative-only: they can query your credits but cannot process images, so both keys are needed to see the balance. If the management key is missing or the query fails, the balance is simply hidden and only the session cost is shown.

### Choosing a Model

The **Models** button in the top bar opens a searchable, selectable list of the OpenRouter models that can process an image and return a processed image, together with the cost of processing a single image with each one. When OpenRouter publishes an exact per-image price for a model it is used; otherwise the cost is estimated from the model's published per-token rates assuming a typical 1024x1024 image plus a generated image output.

### Vectorising Results to SVG

Select the "Vectorise" option if you want your output to be an SVG image, scaleable to fit any size and resolution of media - use your artwork in a book, on a t-shirt or a 96-sheet roadside billboard! This process is done by the application itself, so doesn't need access to an AI processing engine (or even internet access) for this step.

## Running

You can try out Magooify on the [live demo](https://www.sansay.co.uk/magooify/demo/) page.

You can run Magooify on your own system by simply downloading and running the executable for your platform from the project homepage. Simply run the executable, in desktop mode it should open your local web browser and display the user interface.

To use the AI image processing feature, you will need to provide an OpenRouter API key. Without one, the app still works: the prompt editing option is hidden and the captured image is never sent to an AI processing engine, Magooify just acts as a handy image-capture utility.

For more details, or if you are a system administrator wanting to run this application centrally for access by multiple users, see the [documentation](docs/running.md).

## Developers

If you want to use, customise or extend this project - for use by yourself, other users in your school / workplace, or even as a service you charge money for - you are very welcome to. See the [developer's documentation](docs/developers.md) for more details.

This project is distributed under a permissive [Apache 2.0 license](LICENSE) - you are allowed to freely use, modify, and distribute this project for both personal and commercial purposes without paying royalties.
