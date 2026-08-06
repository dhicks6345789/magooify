document.addEventListener('DOMContentLoaded', () => {
  const connBadge = document.getElementById('conn-badge');

  const fileInput = document.getElementById('file-input');
  const previewWrap = document.getElementById('preview-wrap');
  const preview = document.getElementById('preview');
  const noImage = document.getElementById('no-image');
  const btnProcess = document.getElementById('btn-process');
  const processStatus = document.getElementById('process-status');
  const processSpinner = document.getElementById('process-spinner');
  const processLabel = document.getElementById('process-label');
  const promptText = document.getElementById('prompt-text');

  const resultCard = document.getElementById('result-card');
  const resultImage = document.getElementById('result-image');
  const resultFilename = document.getElementById('result-filename');
  const resultModel = document.getElementById('result-model');
  const resultTime = document.getElementById('result-time');
  const btnReset = document.getElementById('btn-reset');

  const dropZone = document.getElementById('drop-zone');
  const previewFrame = document.getElementById('preview-frame');
  const cropBox = document.getElementById('crop-box');
  const cropTip = document.getElementById('crop-tip');
  const btnClearCrop = document.getElementById('btn-clear-crop');

  const cameraModal = document.getElementById('cameraModal');
  const cameraVideo = document.getElementById('camera-video');
  const cameraError = document.getElementById('camera-error');
  const btnCapture = document.getElementById('btn-capture');

  // The model window is roughly 1024x1024; keep the longest side at or below
  // it so the whole image is seen rather than only its top-left corner.
  const MAX_DIM = 1024;

  let cameraStream = null;
  let currentBlob = null;
  let cropRect = null;
  let lastOutputFile = null;

  async function fetchJSON(url, options) {
    const res = await fetch(url, options);
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `Request to ${url} failed (${res.status})`);
    }
    return data;
  }

  function checkConnection() {
    fetchJSON('api/v1/health')
      .then(() => {
        connBadge.classList.remove('text-bg-danger');
        connBadge.classList.add('text-bg-success');
        connBadge.textContent = 'connected';
      })
      .catch(() => {
        connBadge.classList.remove('text-bg-success');
        connBadge.classList.add('text-bg-danger');
        connBadge.textContent = 'offline';
      });
  }

  function loadPrompt() {
    fetchJSON('api/v1/prompt')
      .then((data) => {
        promptText.value = data.prompt || '';
      })
      .catch(() => {});
  }

  function setProcessing(on) {
    btnProcess.disabled = on || !currentBlob;
    processSpinner.classList.toggle('d-none', !on);
    processLabel.textContent = on ? 'Processing...' : 'Process';
  }

  async function startCamera() {
    stopCamera();
    cameraError.classList.add('d-none');
    btnCapture.disabled = true;
    try {
      cameraStream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: 'environment' },
        audio: false,
      });
      cameraVideo.srcObject = cameraStream;
      await cameraVideo.play().catch(() => {});
      btnCapture.disabled = false;
    } catch (err) {
      cameraError.textContent =
        'Could not access the camera: ' + err.message + ' You can still use Upload Image instead.';
      cameraError.classList.remove('d-none');
    }
  }

  function stopCamera() {
    if (cameraStream) {
      cameraStream.getTracks().forEach((track) => track.stop());
      cameraStream = null;
    }
    if (cameraVideo.srcObject) {
      cameraVideo.srcObject = null;
    }
  }

  cameraModal.addEventListener('shown.bs.modal', startCamera);
  cameraModal.addEventListener('hidden.bs.modal', stopCamera);

  btnCapture.addEventListener('click', () => {
    if (!cameraStream || cameraVideo.videoWidth === 0) return;
    const canvas = document.createElement('canvas');
    canvas.width = cameraVideo.videoWidth;
    canvas.height = cameraVideo.videoHeight;
    canvas.getContext('2d').drawImage(cameraVideo, 0, 0);
    canvas.toBlob(
      (blob) => {
        if (blob) setImage(blob, 'camera-capture.jpg');
      },
      'image/jpeg',
      0.92
    );
    const modal = bootstrap.Modal.getInstance(cameraModal);
    if (modal) modal.hide();
  });

  dropZone.addEventListener('click', () => {
    if (previewWrap.classList.contains('d-none')) fileInput.click();
  });

  ['dragenter', 'dragover'].forEach((evt) => {
    dropZone.addEventListener(evt, (e) => {
      e.preventDefault();
      dropZone.classList.add('dragover');
    });
  });

  ['dragleave', 'drop'].forEach((evt) => {
    dropZone.addEventListener(evt, (e) => {
      e.preventDefault();
      dropZone.classList.remove('dragover');
    });
  });

  dropZone.addEventListener('drop', (e) => {
    const file = e.dataTransfer.files[0];
    if (!file) return;
    downscaleImage(file)
      .then((blob) => setImage(blob, file.name))
      .catch((err) => {
        processStatus.textContent = 'Could not read image: ' + err.message;
      });
  });

  fileInput.addEventListener('change', () => {
    const file = fileInput.files[0];
    if (!file) return;
    downscaleImage(file)
      .then((blob) => setImage(blob, file.name))
      .catch((err) => {
        processStatus.textContent = 'Could not read image: ' + err.message;
      });
    fileInput.value = '';
  });

  // imageDisplayRect returns the on-screen rect (in CSS pixels) that the image
  // content actually occupies inside the preview element, after object-fit:
  // contain letterboxing, together with the image's natural dimensions.
  function imageDisplayRect() {
    const nw = preview.naturalWidth;
    const nh = preview.naturalHeight;
    const cw = preview.clientWidth;
    const ch = preview.clientHeight;
    if (!nw || !nh || !cw || !ch) return null;
    const scale = Math.min(cw / nw, ch / nh);
    const w = nw * scale;
    const h = nh * scale;
    return { x: (cw - w) / 2, y: (ch - h) / 2, w, h, nw, nh };
  }

  // toImageCoords maps a pointer position to natural image pixel coordinates.
  function toImageCoords(clientX, clientY) {
    const frameRect = previewFrame.getBoundingClientRect();
    const d = imageDisplayRect();
    if (!d) return null;
    const px = clientX - frameRect.left;
    const py = clientY - frameRect.top;
    return {
      x: Math.max(0, Math.min(d.nw, ((px - d.x) / d.w) * d.nw)),
      y: Math.max(0, Math.min(d.nh, ((py - d.y) / d.h) * d.nh)),
    };
  }

  function clampCrop(c) {
    const nw = preview.naturalWidth;
    const nh = preview.naturalHeight;
    const minW = Math.max(8, nw * 0.02);
    const minH = Math.max(8, nh * 0.02);
    const x = Math.max(0, Math.min(c.x, nw - minW));
    const y = Math.max(0, Math.min(c.y, nh - minH));
    return {
      x,
      y,
      w: Math.max(minW, Math.min(c.w, nw - x)),
      h: Math.max(minH, Math.min(c.h, nh - y)),
    };
  }

  function renderCropBox() {
    const d = imageDisplayRect();
    if (!d || !cropRect) {
      cropBox.style.display = 'none';
      return;
    }
    cropBox.style.display = 'block';
    cropBox.style.left = (cropRect.x / d.nw) * d.w + d.x + 'px';
    cropBox.style.top = (cropRect.y / d.nh) * d.h + d.y + 'px';
    cropBox.style.width = (cropRect.w / d.nw) * d.w + 'px';
    cropBox.style.height = (cropRect.h / d.nh) * d.h + 'px';
  }

  function updateCropTip() {
    if (!cropRect) {
      cropTip.textContent = 'Drag on the image to crop.';
      btnClearCrop.classList.add('d-none');
    } else {
      cropTip.textContent = 'Drag an edge or corner to adjust the crop.';
      btnClearCrop.classList.remove('d-none');
    }
  }

  btnClearCrop.addEventListener('click', () => {
    cropRect = null;
    renderCropBox();
    updateCropTip();
  });

  let cropDrag = null;

  previewFrame.addEventListener('pointerdown', (e) => {
    if (!currentBlob) return;
    const coords = toImageCoords(e.clientX, e.clientY);
    if (!coords) return;
    const handle = e.target.closest('.crop-handle');
    if (handle) {
      cropDrag = {
        mode: 'resize',
        handle: handle.dataset.handle,
        start: coords,
        orig: cropRect ? { ...cropRect } : null,
      };
    } else if (
      cropRect &&
      coords.x >= cropRect.x &&
      coords.x <= cropRect.x + cropRect.w &&
      coords.y >= cropRect.y &&
      coords.y <= cropRect.y + cropRect.h
    ) {
      cropDrag = { mode: 'move', start: coords, orig: { ...cropRect } };
    } else {
      cropDrag = { mode: 'new', start: coords, orig: null };
      cropRect = { x: coords.x, y: coords.y, w: 0, h: 0 };
    }
    previewFrame.setPointerCapture(e.pointerId);
    e.preventDefault();
  });

  previewFrame.addEventListener('pointermove', (e) => {
    if (!cropDrag) return;
    const coords = toImageCoords(e.clientX, e.clientY);
    if (!coords) return;
    if (cropDrag.mode === 'new') {
      cropRect = {
        x: Math.min(cropDrag.start.x, coords.x),
        y: Math.min(cropDrag.start.y, coords.y),
        w: Math.abs(coords.x - cropDrag.start.x),
        h: Math.abs(coords.y - cropDrag.start.y),
      };
    } else if (cropDrag.mode === 'move') {
      cropRect = {
        x: cropDrag.orig.x + coords.x - cropDrag.start.x,
        y: cropDrag.orig.y + coords.y - cropDrag.start.y,
        w: cropDrag.orig.w,
        h: cropDrag.orig.h,
      };
    } else if (cropDrag.mode === 'resize' && cropDrag.orig) {
      const o = cropDrag.orig;
      let { x, y, w, h } = o;
      const hd = cropDrag.handle;
      if (hd.includes('e')) w = coords.x - o.x;
      if (hd.includes('s')) h = coords.y - o.y;
      if (hd.includes('w')) {
        x = coords.x;
        w = o.x + o.w - coords.x;
      }
      if (hd.includes('n')) {
        y = coords.y;
        h = o.y + o.h - coords.y;
      }
      cropRect = { x, y, w, h };
    }
    cropRect = clampCrop(cropRect);
    renderCropBox();
    updateCropTip();
  });

  function endCropDrag() {
    if (!cropDrag) return;
    cropDrag = null;
    if (cropRect && (cropRect.w < 8 || cropRect.h < 8)) {
      cropRect = null;
      renderCropBox();
      updateCropTip();
    }
  }

  previewFrame.addEventListener('pointerup', endCropDrag);
  previewFrame.addEventListener('pointercancel', endCropDrag);
  window.addEventListener('resize', renderCropBox);
  preview.addEventListener('load', renderCropBox);

  async function downscaleImage(file) {
    const bitmap = await createImageBitmap(file);
    const scale = Math.min(1, MAX_DIM / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    canvas.getContext('2d').drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    bitmap.close();
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (blob) => (blob ? resolve(blob) : reject(new Error('Failed to encode image'))),
        'image/jpeg',
        0.92
      );
    });
  }

  function setImage(blob, name) {
    currentBlob = blob;
    cropRect = null;
    cropDrag = null;
    lastOutputFile = null;
    resultCard.classList.add('d-none');
    preview.src = URL.createObjectURL(blob);
    previewWrap.classList.remove('d-none');
    noImage.classList.add('d-none');
    btnProcess.disabled = false;
    processStatus.textContent = name;
    renderCropBox();
    updateCropTip();
  }

  // croppedBlob returns a blob containing only the region inside the crop box.
  // When no crop is set the original blob is returned unchanged.
  async function croppedBlob(blob) {
    if (!cropRect) return blob;
    const bmp = await createImageBitmap(blob);
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(cropRect.w));
    canvas.height = Math.max(1, Math.round(cropRect.h));
    canvas.getContext('2d').drawImage(
      bmp,
      cropRect.x,
      cropRect.y,
      cropRect.w,
      cropRect.h,
      0,
      0,
      canvas.width,
      canvas.height
    );
    bmp.close();
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (b) => (b ? resolve(b) : reject(new Error('Failed to encode image'))),
        'image/jpeg',
        0.92
      );
    });
  }

  btnProcess.addEventListener('click', async () => {
    if (!currentBlob) return;
    setProcessing(true);
    processStatus.textContent = 'Processing...';
    try {
      const sendBlob = await croppedBlob(currentBlob);
      const form = new FormData();
      form.append('image', sendBlob, 'capture.jpg');
      const prompt = promptText.value.trim();
      if (prompt) form.append('prompt', prompt);
      if (lastOutputFile) form.append('output', lastOutputFile);
      const data = await fetchJSON('api/v1/process', { method: 'POST', body: form });
      lastOutputFile = data.filename;
      resultImage.src = 'api/v1/images/' + encodeURIComponent(data.filename);
      resultFilename.textContent = data.filename;
      resultModel.textContent = data.model;
      resultTime.textContent = new Date(data.processed_at).toLocaleString();
      resultCard.classList.remove('d-none');
      resultCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
      processStatus.textContent = 'Done';
    } catch (err) {
      processStatus.textContent = 'Failed: ' + err.message;
    } finally {
      setProcessing(false);
    }
  });

  btnReset.addEventListener('click', () => {
    currentBlob = null;
    cropRect = null;
    cropDrag = null;
    lastOutputFile = null;
    preview.src = '';
    previewWrap.classList.add('d-none');
    noImage.classList.remove('d-none');
    resultCard.classList.add('d-none');
    resultImage.src = '';
    processStatus.textContent = '';
    setProcessing(false);
    btnProcess.disabled = true;
    loadPrompt();
  });

  checkConnection();
  loadPrompt();
});
