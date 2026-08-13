document.addEventListener('DOMContentLoaded', () => {
  const connBadge = document.getElementById('conn-badge');
  const creditInfo = document.getElementById('credit-info');

  // Theme toggle: light is the default, a saved preference wins, and the
  // choice is remembered for the next visit.
  const themeToggle = document.getElementById('theme-toggle');
  const themeIconMoon = document.getElementById('theme-icon-moon');
  const themeIconSun = document.getElementById('theme-icon-sun');

  const applyTheme = (theme) => {
    document.documentElement.dataset.bsTheme = theme;
    themeToggle.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
    themeIconMoon.classList.toggle('d-none', theme === 'dark');
    themeIconSun.classList.toggle('d-none', theme === 'light');
  };

  let savedTheme = null;
  try {
    savedTheme = localStorage.getItem('magooify-theme');
  } catch (e) {
    /* localStorage unavailable; keep the default */
  }
  applyTheme(savedTheme === 'dark' ? 'dark' : 'light');

  themeToggle.addEventListener('click', () => {
    const next = document.documentElement.dataset.bsTheme === 'dark' ? 'light' : 'dark';
    applyTheme(next);
    try {
      localStorage.setItem('magooify-theme', next);
    } catch (e) {
      /* storage unavailable; the choice just won't persist */
    }
  });

  // OpenRouter balances are US dollars; format them with the browser's locale
  // so separators, symbol placement and formatting follow local conventions.
  const currencyFmt = new Intl.NumberFormat(navigator.language || 'en', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 4,
  });
  const perMillionFmt = new Intl.NumberFormat(navigator.language || 'en', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 2,
  });

  const modelsModal = document.getElementById('modelsModal');
  const modelsSearch = document.getElementById('models-search');
  const modelsTbody = document.getElementById('models-tbody');
  const modelsCount = document.getElementById('models-count');
  let modelsList = null;
  let currentModelId = null;
  let selectingModel = false;

  // openrouterConfigured mirrors the openrouter_configured field reported by
  // /api/v1/info: when false the UI hides AI-only controls (the prompt editor
  // and the Models picker) so the captured image is never sent to OpenRouter.
  // The Vectorise switch and the palette selector stay fully interactive so
  // the app can still be used to trace captured images into SVG, or to scan
  // bitmap images directly. The default of true keeps the UI fully functional
  // if /api/v1/info is slow or briefly unreachable; the value is overwritten
  // as soon as the response arrives.
  let openrouterConfigured = true;

  const fileInput = document.getElementById('file-input');
  const previewWrap = document.getElementById('preview-wrap');
  const preview = document.getElementById('preview');
  const noImage = document.getElementById('no-image');
  const btnProcess = document.getElementById('btn-process');
  const processStatus = document.getElementById('process-status');
  const processSpinner = document.getElementById('process-spinner');
  const processLabel = document.getElementById('process-label');
  const promptText = document.getElementById('prompt-text');

  // basePrompt holds the prompt returned by /api/v1/prompt (the configurable
  // default = PROMPT.md or -prompt-file). It is the starting point the
  // palette-specific line is appended to, mirroring applyPalettePrompt in
  // api.go so the textarea always shows what the request will actually send.
  let basePrompt = '';

  // palettePromptRe matches the same "Limit the colour palette to N colours,
  // rendering any colours found to their nearest-fit colour." sentence the
  // backend strips before adding the palette-version of the line.
  const palettePromptRe = /\s*Limit the colour palette to[^.]*\.\s*/i;

  function composePromptWithPalette(base, p) {
    if (!base) {
      return p ? palettePromptLine(p) : '';
    }
    if (!p) {
      return base;
    }
    const trimmed = base.replace(palettePromptRe, ' ').replace(/\s+$/, '');
    const sep = trimmed && !trimmed.endsWith('.') ? '. ' : ' ';
    return trimmed + sep + palettePromptLine(p);
  }

  function palettePromptLine(p) {
    const hexes = p.colours.map((c) => c.hex.toLowerCase()).join(', ');
    return 'Limit the colour palette to white plus these ' +
      p.colours.length + ' colours: ' + hexes +
      ', rendering any colours found to their nearest-fit colour.';
  }

  function refreshPromptPreview() {
    const current = paletteCheck.checked
      ? palettes.find((x) => x.id === paletteSelect.value)
      : null;
    promptText.value = composePromptWithPalette(basePrompt, current);
  }
  const vectoriseCheck = document.getElementById('chk-vectorise');
  const paletteCheck = document.getElementById('chk-palette');
  const paletteCollapse = document.getElementById('palette-collapse');
  const paletteSelect = document.getElementById('sel-palette');
  const paletteSwatch = document.getElementById('palette-swatch');
  const paletteHint = document.getElementById('palette-hint');

  // refreshPaletteHint nudges the user when their palette selection will have
  // no effect on the output: with no OpenRouter API key and Convert result to
  // SVG off, the palette id is ignored entirely, so the captured bitmap is the
  // same as the input. Keeping the hint contextual lets the palette feel
  // "magical" only when it actually changes something.
  function refreshPaletteHint() {
    const ignored =
      !openrouterConfigured && paletteCheck.checked && !vectoriseCheck.checked;
    if (paletteHint) {
      paletteHint.classList.toggle('d-none', !ignored);
    }
  }

  let palettes = [];

  const resultCard = document.getElementById('result-card');
  const resultImage = document.getElementById('result-image');
  const resultDownload = document.getElementById('result-download');
  const resultDownloadLabel = document.getElementById('result-download-label');
  const resultModel = document.getElementById('result-model');
  const resultTime = document.getElementById('result-time');
  const btnReset = document.getElementById('btn-reset');

  const dropZone = document.getElementById('drop-zone');
  const previewFrame = document.getElementById('preview-frame');
  const cropBox = document.getElementById('crop-box');
  const cropTip = document.getElementById('crop-tip');
  const btnClearCrop = document.getElementById('btn-clear-crop');

  // Image-transform buttons appear under the crop-tip row. They let the user
  // re-orient the captured image locally before processing - the rotation and
  // mirror operations are pure canvas ops, so the captured image never leaves
  // the browser. The buttons are wired up in the init block below.
  const btnRotateLeft = document.getElementById('btn-rotate-left');
  const btnMirrorVertical = document.getElementById('btn-mirror-vertical');
  const btnMirrorHorizontal = document.getElementById('btn-mirror-horizontal');
  const btnRotateRight = document.getElementById('btn-rotate-right');
  const transformButtons = [btnRotateLeft, btnMirrorVertical, btnMirrorHorizontal, btnRotateRight];

  const cameraModal = document.getElementById('cameraModal');
  const cameraVideo = document.getElementById('camera-video');
  const cameraError = document.getElementById('camera-error');
  const cameraSelect = document.getElementById('camera-select');
  const btnCapture = document.getElementById('btn-capture');

  // The model window is roughly 1024x1024; keep the longest side at or below
  // it so the whole image is seen rather than only its top-left corner.
  const MAX_DIM = 1024;

  // Each click on a rotate button nudges the captured image by this many
  // degrees. Small steps let the user fine-tune the orientation in the
  // browser before processing it; clicking the same button repeatedly
  // accumulates to larger angles.
  const ROTATE_DEG = 5;

  let cameraStream = null;
  let currentBlob = null;
  let currentName = '';
  let cropRect = null;
  let lastOutputFile = null;
  let cameraDevices = [];

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
        basePrompt = (data.prompt || '').trim();
        refreshPromptPreview();
      })
      .catch(() => {});
  }

  // Group palettes by brand for the dropdown so it's easy to find the entry a
  // user is after; the option text reads "Brand – N-pack".
  function paletteOptionLabel(p) {
    return p.brand + ' – ' + p.name.replace(/.*?(\d+-pack)/, '$1');
  }

  function renderPaletteSwatch(p) {
    if (!p) {
      paletteSwatch.textContent = '';
      return;
    }
    const dots = p.colours.map((c) => '<span title="' + c.name + '" style="display:inline-block;width:1rem;height:1rem;border-radius:50%;background:' + c.hex + ';border:1px solid rgba(0,0,0,.2);margin-right:.25rem;"></span>').join('');
    paletteSwatch.innerHTML = '<div class="d-flex flex-wrap align-items-center gap-1 mt-1">' + dots + '</div>';
  }

  const defaultPaletteID = 'berol-24';

  function loadPalettes() {
    fetchJSON('api/v1/palettes')
      .then((data) => {
        palettes = Array.isArray(data.palettes) ? data.palettes : [];
        const groups = {};
        for (const p of palettes) {
          (groups[p.brand] = groups[p.brand] || []).push(p);
        }
        const frag = document.createDocumentFragment();
        for (const brand of Object.keys(groups).sort()) {
          const og = document.createElement('optgroup');
          og.label = brand;
          groups[brand].forEach((p) => {
            const opt = document.createElement('option');
            opt.value = p.id;
            opt.textContent = paletteOptionLabel(p);
            if (p.id === defaultPaletteID) {
              opt.selected = true;
            }
            og.appendChild(opt);
          });
          frag.appendChild(og);
        }
        paletteSelect.innerHTML = '';
        paletteSelect.appendChild(frag);
        const def = palettes.find((p) => p.id === defaultPaletteID) || palettes[0];
        renderPaletteSwatch(def);
        refreshPromptPreview();
      })
      .catch(() => {
        paletteSelect.innerHTML = '';
        paletteSwatch.textContent = 'Palette list unavailable.';
      });
  }

  paletteCheck.addEventListener('change', () => {
    const shown = paletteCheck.checked;
    paletteCollapse.classList.toggle('show', shown);
    refreshPromptPreview();
    refreshPaletteHint();
  });

  vectoriseCheck.addEventListener('change', () => {
    refreshPaletteHint();
  });

  paletteSelect.addEventListener('change', () => {
    const p = palettes.find((x) => x.id === paletteSelect.value);
    renderPaletteSwatch(p);
    refreshPromptPreview();
  });

  function loadCredits() {
    // Skip the OpenRouter credits lookup entirely when no key is configured;
    // there's no session cost to report and no balance to show.
    if (!openrouterConfigured) {
      creditInfo.textContent = '';
      return;
    }
    fetchJSON('api/v1/credits')
      .then((data) => {
        const parts = ['Session: ' + currencyFmt.format(data.session_cost || 0)];
        if (data.credits_available) {
          parts.unshift('Credits: ' + currencyFmt.format(data.remaining_credits || 0));
        }
        creditInfo.textContent = parts.join(' · ');
      })
      .catch(() => {
        creditInfo.textContent = '';
      });
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;',
      "'": '&#39;',
    }[c]));
  }

  function loadInfo() {
    fetchJSON('api/v1/info')
      .then((data) => {
        currentModelId = data.model || null;
        openrouterConfigured = data.openrouter_configured === true;
        applyFeatureVisibility();
      })
      .catch(() => {});
  }

  // applyFeatureVisibility shows or hides the AI-only controls - the prompt
  // editor, the Models picker and the credit balance - based on whether the
  // OpenRouter API key has been configured. The Vectorise switch stays
  // interactive in both modes so the user can pick between just scanning the
  // bitmap and tracing it into an SVG.
  function applyFeatureVisibility() {
    document.querySelectorAll('[data-feature="openrouter"]').forEach((el) => {
      el.classList.toggle('d-none', !openrouterConfigured);
    });
    refreshPromptPreview();
    refreshPaletteHint();
  }

  async function loadModels() {
    const data = await fetchJSON('api/v1/models');
    modelsList = data.models || [];
    renderModels();
  }

  function filteredModels() {
    const q = modelsSearch.value.trim().toLowerCase();
    return modelsList.filter((m) => {
      if (!q) return true;
      return m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q);
    });
  }

  function renderModels() {
    if (!modelsList) return;
    const rows = filteredModels();
    modelsCount.textContent = rows.length + ' models';
    modelsTbody.innerHTML = '';
    for (const m of rows) {
      const tr = document.createElement('tr');
      const active = m.id === currentModelId
        ? ' <span class="badge text-bg-primary">active</span>'
        : '';
      tr.title = 'Use ' + m.id;
      tr.innerHTML =
        '<td>' +
        '<div>' + escapeHTML(m.name) + active + '</div>' +
        '<div class="font-monospace small text-secondary">' + escapeHTML(m.id) + '</div>' +
        '</td>' +
        '<td class="text-end">' + currencyFmt.format(m.estimated_image_cost || 0) + '</td>' +
        '<td class="text-end">' + perMillionFmt.format(m.prompt_per_million || 0) + '</td>' +
        '<td class="text-end">' + perMillionFmt.format(m.completion_per_million || 0) + '</td>';
      tr.addEventListener('click', () => selectModel(m));
      modelsTbody.appendChild(tr);
    }
  }

  async function selectModel(m) {
    if (selectingModel || m.id === currentModelId) return;
    selectingModel = true;
    modelsCount.textContent = 'Switching to ' + m.id + '...';
    try {
      const data = await fetchJSON('api/v1/model', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: m.id }),
      });
      currentModelId = data.model || m.id;
      renderModels();
      modelsCount.textContent =
        filteredModels().length + ' models · now using ' + currentModelId;
    } catch (err) {
      modelsCount.textContent = 'Failed to switch model: ' + err.message;
    } finally {
      selectingModel = false;
    }
  }

  modelsModal.addEventListener('shown.bs.modal', () => {
    modelsTbody.innerHTML =
      '<tr><td colspan="4" class="text-center text-secondary py-4">Loading models...</td></tr>';
    modelsCount.textContent = '';
    loadModels().catch((err) => {
      modelsTbody.innerHTML =
        '<tr><td colspan="4" class="text-center text-danger py-4">Failed to load models: ' +
        escapeHTML(err.message) + '</td></tr>';
    });
  });
  modelsSearch.addEventListener('input', renderModels);

  function setProcessing(on) {
    // Disable every image-affecting button while a Process request is in
    // flight, mirroring how Process itself is gated, so an in-flight
    // request doesn't see the underlying blob swapped out from under it.
    setImageActionButtonsEnabled(!on && !!currentBlob);
    processSpinner.classList.toggle('d-none', !on);
    processLabel.textContent = on ? 'Processing...' : 'Process';
  }

  // setImageActionButtonsEnabled toggles Process, the four transform buttons
  // and the camera / file / drop-zone-entry points in lock-step so the user
  // can only act on the captured image when there is one.
  function setImageActionButtonsEnabled(on) {
    btnProcess.disabled = !on;
    transformButtons.forEach((b) => { b.disabled = !on; });
  }

  async function populateCameraSelect() {
    let devices = [];
    try {
      devices = await navigator.mediaDevices.enumerateDevices();
    } catch (err) {
      return;
    }
    cameraDevices = devices.filter((d) => d.kind === 'videoinput');
    const previous = cameraSelect.value;
    cameraSelect.innerHTML = '';
    if (cameraDevices.length < 2) {
      cameraSelect.disabled = true;
      cameraSelect.innerHTML = '<option value="">Default camera</option>';
      return;
    }
    cameraSelect.disabled = false;
    cameraDevices.forEach((device, idx) => {
      const label = device.label || 'Camera ' + (idx + 1);
      const opt = document.createElement('option');
      opt.value = device.deviceId;
      opt.textContent = label;
      cameraSelect.appendChild(opt);
    });
    if (previous && cameraDevices.some((d) => d.deviceId === previous)) {
      cameraSelect.value = previous;
    }
  }

  async function startCamera() {
    stopCamera();
    cameraError.classList.add('d-none');
    btnCapture.disabled = true;
    await populateCameraSelect();
    try {
      let videoConstraints = { facingMode: 'environment' };
      if (cameraSelect.value && cameraDevices.some((d) => d.deviceId === cameraSelect.value)) {
        videoConstraints = { deviceId: { exact: cameraSelect.value } };
      }
      cameraStream = await navigator.mediaDevices.getUserMedia({
        video: videoConstraints,
        audio: false,
      });
      cameraVideo.srcObject = cameraStream;
      await cameraVideo.play().catch(() => {});
      // Device labels are only populated once permission is granted, so refresh
      // the list now that the stream is running.
      await populateCameraSelect();
      btnCapture.disabled = false;
    } catch (err) {
      cameraError.textContent =
        'Could not access the camera: ' + err.message + ' You can still use Upload Image instead.';
      cameraError.classList.remove('d-none');
    }
  }

  cameraSelect.addEventListener('change', startCamera);

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
    // Transforms re-use the same setImage entry point without supplying a
    // name; in that case keep the original file label so the status line
    // (and the eventual processed filename) still describes where the
    // image came from.
    if (name !== undefined) {
      currentName = name;
    }
    cropRect = null;
    cropDrag = null;
    lastOutputFile = null;
    resultCard.classList.add('d-none');
    preview.src = URL.createObjectURL(blob);
    previewWrap.classList.remove('d-none');
    noImage.classList.add('d-none');
    setImageActionButtonsEnabled(true);
    processStatus.textContent = currentName;
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

  // canvasToBlob encodes a canvas as JPEG, mirroring the encoding the rest of
  // the app uses so the resulting blob is interchangeable with anything the
  // capture / upload paths produce.
  function canvasToBlob(canvas) {
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (b) => (b ? resolve(b) : reject(new Error('Failed to encode image'))),
        'image/jpeg',
        0.92
      );
    });
  }

  // loadImageBlob wraps the blob in an object URL and resolves with a loaded
  // <img>. Doing this via <img> rather than createImageBitmap means SVG (and
  // any other format the browser can render) is supported too.
  function loadImageBlob(blob) {
    const url = URL.createObjectURL(blob);
    return new Promise((resolve, reject) => {
      const img = new Image();
      img.onload = () => {
        URL.revokeObjectURL(url);
        resolve(img);
      };
      img.onerror = () => {
        URL.revokeObjectURL(url);
        reject(new Error('Failed to load image'));
      };
      img.src = url;
    });
  }

  // rotateImage turns the captured image by the given number of degrees
  // (positive is clockwise in canvas coordinates, negative is
  // anti-clockwise) and returns the result as a JPEG blob. The canvas is
  // sized large enough to hold the rotated image's bounding rectangle and
  // the image is drawn centred so successive small rotations accumulate
  // cleanly without drifting to one side.
  async function rotateImage(blob, degrees) {
    const img = await loadImageBlob(blob);
    const w = img.naturalWidth;
    const h = img.naturalHeight;
    const rad = degrees * Math.PI / 180;
    const absSin = Math.abs(Math.sin(rad));
    const absCos = Math.abs(Math.cos(rad));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(w * absCos + h * absSin));
    canvas.height = Math.max(1, Math.round(w * absSin + h * absCos));
    const ctx = canvas.getContext('2d');
    ctx.translate(canvas.width / 2, canvas.height / 2);
    ctx.rotate(rad);
    ctx.drawImage(img, -w / 2, -h / 2);
    return canvasToBlob(canvas);
  }

  // mirrorImage flips the captured image along the given axis: 'x' mirrors
  // across a vertical line (left/right swap) and 'y' mirrors across a
  // horizontal line (top/bottom swap). The canvas dimensions are unchanged.
  async function mirrorImage(blob, axis) {
    const img = await loadImageBlob(blob);
    const canvas = document.createElement('canvas');
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    const ctx = canvas.getContext('2d');
    if (axis === 'x') {
      ctx.translate(canvas.width, 0);
      ctx.scale(-1, 1);
    } else {
      ctx.translate(0, canvas.height);
      ctx.scale(1, -1);
    }
    ctx.drawImage(img, 0, 0);
    return canvasToBlob(canvas);
  }

  // applyTransform runs a transform against a stable snapshot of the current
  // blob so the rest of the UI sees a single consistent blob change even if
  // another click happens mid-flight. On failure the blob is left untouched
  // and the error surfaces in the status line.
  async function applyTransform(transform) {
    if (!currentBlob) return;
    const snapshot = currentBlob;
    try {
      const next = await transform(snapshot);
      setImage(next);
    } catch (err) {
      processStatus.textContent = 'Transform failed: ' + err.message;
    }
  }

  btnRotateLeft.addEventListener('click', () => {
    applyTransform((blob) => rotateImage(blob, -ROTATE_DEG));
  });

  btnRotateRight.addEventListener('click', () => {
    applyTransform((blob) => rotateImage(blob, ROTATE_DEG));
  });

  btnMirrorVertical.addEventListener('click', () => {
    applyTransform((blob) => mirrorImage(blob, 'x'));
  });

  btnMirrorHorizontal.addEventListener('click', () => {
    applyTransform((blob) => mirrorImage(blob, 'y'));
  });

  btnProcess.addEventListener('click', async () => {
    if (!currentBlob) return;
    setProcessing(true);
    processStatus.textContent = processStatusText('start');
    try {
      const sendBlob = await croppedBlob(currentBlob);
      const form = new FormData();
      form.append('image', sendBlob, 'capture.jpg');
      // Skip the prompt when OpenRouter isn't configured: there's no model to
      // send it to and the prompt editor is hidden anyway.
      if (openrouterConfigured) {
        const prompt = promptText.value.trim();
        if (prompt) form.append('prompt', prompt);
      }
      if (lastOutputFile) form.append('output', lastOutputFile);
      if (vectoriseCheck.checked) form.append('vectorise', 'true');
      if (paletteCheck.checked) form.append('palette', paletteSelect.value);
      const data = await fetchJSON('api/v1/process', { method: 'POST', body: form });
      lastOutputFile = data.filename;
      resultImage.src =
        'api/v1/images/' +
        encodeURIComponent(data.filename) +
        '?t=' +
        encodeURIComponent(data.processed_at);
      resultImage.classList.toggle('vector', data.vectorised === true);
      resultDownloadLabel.textContent = 'Download ' + data.filename;
      resultDownload.href = 'api/v1/images/' + encodeURIComponent(data.filename);
      resultDownload.setAttribute('download', data.filename);
      resultModel.textContent = data.model || resultModelFallback();
      resultTime.textContent = new Date(data.processed_at).toLocaleString();
      resultCard.classList.remove('d-none');
      resultCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
      processStatus.textContent = 'Done';
      if (openrouterConfigured) {
        loadCredits();
      }
    } catch (err) {
      processStatus.textContent = 'Failed: ' + err.message;
    } finally {
      setProcessing(false);
    }
  });

  // processStatusText picks a sensible status line based on whether OpenRouter
  // is configured and whether the Vectorise switch is on, so the user can see
  // what's happening to their image (AI processing, local vectorisation or a
  // plain bitmap scan) without needing to inspect the response.
  function processStatusText(phase) {
    if (phase !== 'start') return phase;
    if (!openrouterConfigured) {
      return vectoriseCheck.checked ? 'Vectorising...' : 'Saving...';
    }
    return vectoriseCheck.checked ? 'Processing & vectorising...' : 'Processing...';
  }

  // resultModelFallback is shown in the result card in place of a model name
  // when no AI was involved - either a local vectorisation or a plain
  // bitmap scan.
  function resultModelFallback() {
    if (!openrouterConfigured) {
      return vectoriseCheck.checked ? 'Local vectorisation' : 'Bitmap scan';
    }
    return '';
  }

  btnReset.addEventListener('click', () => {
    currentBlob = null;
    currentName = '';
    cropRect = null;
    cropDrag = null;
    lastOutputFile = null;
    preview.src = '';
    previewWrap.classList.add('d-none');
    noImage.classList.remove('d-none');
    resultCard.classList.add('d-none');
    resultImage.src = '';
    resultImage.classList.remove('vector');
    processStatus.textContent = '';
    setProcessing(false);
    setImageActionButtonsEnabled(false);
    loadPrompt();
  });

  checkConnection();
  loadPrompt();
  loadPalettes();
  loadCredits();
  loadInfo();
});
