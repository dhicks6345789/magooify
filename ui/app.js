document.addEventListener('DOMContentLoaded', () => {
  const connBadge = document.getElementById('conn-badge');

  const btnUpload = document.getElementById('btn-upload');
  const fileInput = document.getElementById('file-input');
  const previewWrap = document.getElementById('preview-wrap');
  const preview = document.getElementById('preview');
  const noImage = document.getElementById('no-image');
  const btnProcess = document.getElementById('btn-process');
  const processStatus = document.getElementById('process-status');

  const resultCard = document.getElementById('result-card');
  const resultImage = document.getElementById('result-image');
  const resultDescription = document.getElementById('result-description');
  const resultFilename = document.getElementById('result-filename');
  const resultModel = document.getElementById('result-model');
  const resultTime = document.getElementById('result-time');

  const cameraModal = document.getElementById('cameraModal');
  const cameraVideo = document.getElementById('camera-video');
  const cameraError = document.getElementById('camera-error');
  const btnCapture = document.getElementById('btn-capture');

  const MAX_DIM = 1280;

  let cameraStream = null;
  let currentBlob = null;

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
    processStatus.textContent = name;
  }

  btnProcess.addEventListener('click', async () => {
    if (!currentBlob) return;
    btnProcess.disabled = true;
    processStatus.textContent = 'Processing...';
    try {
      const form = new FormData();
      form.append('image', currentBlob, 'capture.jpg');
      const data = await fetchJSON('api/v1/process', { method: 'POST', body: form });
      resultImage.src = 'api/v1/images/' + encodeURIComponent(data.filename);
      resultDescription.textContent = data.description;
      resultFilename.textContent = data.filename;
      resultModel.textContent = data.model;
      resultTime.textContent = new Date(data.processed_at).toLocaleString();
      resultCard.classList.remove('d-none');
      resultCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
      processStatus.textContent = 'Done';
    } catch (err) {
      processStatus.textContent = 'Failed: ' + err.message;
    } finally {
      btnProcess.disabled = false;
    }
  });

  checkConnection();
});
