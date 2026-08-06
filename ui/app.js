document.addEventListener('DOMContentLoaded', () => {
  const userBadge = document.getElementById('user-badge');
  const userName = document.getElementById('user-name');
  const userMode = document.getElementById('user-mode');
  const connBadge = document.getElementById('conn-badge');

  const btnCamera = document.getElementById('btn-camera');
  const btnUpload = document.getElementById('btn-upload');
  const fileInput = document.getElementById('file-input');
  const previewWrap = document.getElementById('preview-wrap');
  const preview = document.getElementById('preview');
  const noImage = document.getElementById('no-image');
  const btnProcess = document.getElementById('btn-process');
  const processStatus = document.getElementById('process-status');

  const resultPlaceholder = document.getElementById('result-placeholder');
  const resultBody = document.getElementById('result-body');
  const resultDescription = document.getElementById('result-description');
  const resultFilename = document.getElementById('result-filename');
  const resultTextfile = document.getElementById('result-textfile');
  const resultModel = document.getElementById('result-model');
  const resultTime = document.getElementById('result-time');

  const gallery = document.getElementById('gallery');
  const galleryEmpty = document.getElementById('gallery-empty');
  const btnRefresh = document.getElementById('btn-refresh');

  const cameraModal = document.getElementById('cameraModal');
  const cameraVideo = document.getElementById('camera-video');
  const cameraError = document.getElementById('camera-error');
  const btnCapture = document.getElementById('btn-capture');

  const MAX_DIM = 1280;

  let cameraStream = null;
  let currentBlob = null;

  function escapeHtml(value) {
    const div = document.createElement('div');
    div.textContent = value == null ? '' : String(value);
    return div.innerHTML;
  }

  async function fetchJSON(url, options) {
    const res = await fetch(url, options);
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `Request to ${url} failed (${res.status})`);
    }
    return data;
  }

  async function loadUser() {
    try {
      const data = await fetchJSON('api/v1/user');
      userName.textContent = data.username;
      userMode.textContent = `${data.mode} mode (${data.auth_type})`;
      connBadge.classList.remove('text-bg-danger');
      connBadge.classList.add('text-bg-success');
      connBadge.textContent = 'connected';
      userBadge.style.opacity = '1';
    } catch (err) {
      userName.textContent = 'Offline / Disconnected';
      userMode.textContent = 'connection failed';
      connBadge.classList.remove('text-bg-success');
      connBadge.classList.add('text-bg-danger');
      connBadge.textContent = 'disconnected';
    }
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

  btnUpload.addEventListener('click', () => fileInput.click());

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

  async function downscaleImage(file) {
    const bitmap = await createImageBitmap(file);
    const scale = Math.min(1, MAX_DIM / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    canvas.getContext('2d').drawImage(bitmap, 0, 0);
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
    preview.src = URL.createObjectURL(blob);
    previewWrap.classList.remove('d-none');
    noImage.classList.add('d-none');
    btnProcess.disabled = false;
    processStatus.textContent = `Ready to process: ${name} (${(blob.size / 1024).toFixed(1)} KB).`;
  }

  btnProcess.addEventListener('click', async () => {
    if (!currentBlob) return;
    btnProcess.disabled = true;
    processStatus.textContent = 'Sending image to OpenRouter for processing...';
    try {
      const form = new FormData();
      form.append('image', currentBlob, 'capture.jpg');
      const data = await fetchJSON('api/v1/process', { method: 'POST', body: form });
      resultDescription.textContent = data.description;
      resultFilename.textContent = data.filename;
      resultTextfile.textContent = data.text_file;
      resultModel.textContent = data.model;
      resultTime.textContent = new Date(data.processed_at).toLocaleString();
      resultPlaceholder.classList.add('d-none');
      resultBody.classList.remove('d-none');
      processStatus.textContent = 'Processing complete.';
      loadGallery();
    } catch (err) {
      processStatus.textContent = 'Processing failed: ' + err.message;
    } finally {
      btnProcess.disabled = false;
    }
  });

  async function loadGallery() {
    try {
      const data = await fetchJSON('api/v1/images');
      const images = data.images || [];
      gallery.innerHTML = '';
      galleryEmpty.classList.toggle('d-none', images.length > 0);
      images.forEach((img) => {
        const col = document.createElement('div');
        col.className = 'col-sm-6 col-md-4 col-xl-3';
        const href = 'api/v1/images/' + encodeURIComponent(img.filename);
        const thumb = img.description
          ? `<p class="small mb-0 gallery-desc">${escapeHtml(img.description)}</p>`
          : '';
        col.innerHTML =
          `<div class="card h-100 gallery-card">
             <a href="${href}" target="_blank" rel="noopener" class="gallery-thumb">
               <img src="${href}" alt="${escapeHtml(img.filename)}" loading="lazy" class="card-img-top" />
             </a>
             <div class="card-body">
               <div class="small text-secondary mb-1 font-monospace text-truncate">${escapeHtml(img.filename)}</div>
               ${thumb}
             </div>
           </div>`;
        gallery.appendChild(col);
      });
    } catch (err) {
      galleryEmpty.classList.remove('d-none');
      galleryEmpty.textContent = 'Could not load stored images.';
    }
  }

  btnRefresh.addEventListener('click', loadGallery);

  loadUser();
  loadGallery();
});
