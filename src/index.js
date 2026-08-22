/**
 * partage - © 2025 Nicolas CARPi, Deltablot
 */
import { formatUnixTimestamp, formatSize, mkSpin } from './utils.js';
import { Partage } from './partage.js';

document.addEventListener('DOMContentLoaded', function() {

  document.querySelectorAll('.toggle-eye').forEach(btn => {
    btn.addEventListener('click', () => {
      const input = btn.previousElementSibling;
      if (input.type === 'password') {
        input.type = 'text';
        btn.setAttribute('aria-label', 'Hide passphrase');
      } else {
        input.type = 'password';
        btn.setAttribute('aria-label', 'Show passphrase');
      }
    });
  });

  const generatePassphraseBtn = document.querySelector('.generate-passphrase');
  if (generatePassphraseBtn) {
    generatePassphraseBtn.addEventListener('click', () => {
      generatePassphraseBtn.parentElement.querySelector('input').value = crypto.randomUUID();
    });
  }
  const partage = new Partage();

  const errorDialog = document.getElementById('error-dialog');
  const closeButton = errorDialog.querySelector("button");
  closeButton.addEventListener("click", () => {
    errorDialog.close();
  });

  const tosLink = document.getElementById('tos-link');
  const tosDialog = document.getElementById('tos');
  tosLink.addEventListener("click", () => {
    tosDialog.showModal();
  });
  const closeButtonTos = tosDialog.querySelector("button");
  closeButtonTos.addEventListener("click", () => {
    tosDialog.close();
  });

  // INDEX
  const form = document.getElementById('uploadForm');
  if (form) {
    form.addEventListener('submit', async function(event) {
      event.preventDefault();

      // detect cancel button being clicked
      const submitterType = event.submitter.getAttribute('type');
      if (submitterType === 'cancel') {
        form.reset();
        return;
      }

      // change the button to show progress
      mkSpin(form.querySelector('button[type="submit"]'));

      // get file
      const fileInput = form.querySelector('input[type="file"]');
      let file = {
        type: 'application/x-empty',
        name: undefined,
      }
      if (fileInput.files.length > 0) {
        file = fileInput.files[0];
      }

      // get text
      const text = form.querySelector('textarea').value;

      // get passphrase
      let passphraseInUrl = '';
      let passphrase = document.querySelector('input[name="passphrase"]').value;
      if (!passphrase) {
        // just grab the last part of it, we don't need the full randomness of uuid
        passphrase = crypto.randomUUID().split('-')[4];
        passphraseInUrl = `.${passphrase}`;
      }

      let encryptedUpload;
      try {
        // Encrypt in chunks. Large ciphertexts are staged in browser disk storage so
        // the JS heap never needs to hold the complete file or ciphertext at once.
        encryptedUpload = await partage.getEncryptedBlob(file, text, passphrase);

        // send the encrypted data for storage
        const formData = new FormData();
        // send the key in a custom header
        const partageKey = document.getElementById('x-partage-key').innerText;
        const deadline = document.querySelector('select[name="deadline"]').value;
        const headers = new Headers({'X-Partage-Key': partageKey});
        formData.append("file", encryptedUpload.blob, "partage");
        formData.append("deadline", deadline);
        const response = await fetch("/api/v1/parts", {
          method: "POST",
          body: formData,
          headers: headers,
        });
        if (response.ok) {
          const json = await response.json();
          const linkDiv = document.getElementById('linkDiv');
          const btn = document.querySelector('[data-action="copy"]');
          const status = document.getElementById('status');
          const btnWrapper = document.getElementById('clipboard-wrapper');
          document.getElementById('anotherDiv').removeAttribute('hidden');
          linkDiv.removeAttribute('hidden');
          btn.removeAttribute('hidden');
          btnWrapper.removeAttribute('hidden');
          const link = document.createElement('input');
          const expiresAt = Number(json.expires_at).toString(36);
          const linkUrl = `${document.location}get#${json.id}.${expiresAt}${passphraseInUrl}`;
          link.value = linkUrl;
          link.setAttribute('readonly', 'readonly');
          link.addEventListener('focus', async () => {
            link.select();
            await navigator.clipboard.writeText(link.value);
          });
          link.addEventListener('click', async () => {
            link.select();
            await navigator.clipboard.writeText(link.value);
          });
          link.innerText = linkUrl;
          link.classList.add('get-link');
          linkDiv.innerText = '';
          linkDiv.appendChild(link);
          linkDiv.appendChild(btnWrapper);
          btnWrapper.appendChild(status);
          btn.addEventListener('click', async () => {
            await navigator.clipboard.writeText(link.value);
            btn.textContent = '✔';
            status.textContent = 'Copied to clipboard';
            setTimeout(() => {
              btn.textContent = '⧉';
              status.textContent = '';
            }, 2000);
          });
          form.remove();
          let subtitle = 'Copy this link and send it by email. It is recommended to send the passphrase through a different channel';
          if (passphraseInUrl) {
            subtitle = 'Copy this link and send it by email';
          }
          document.getElementById('subtitle').innerText = subtitle;
        } else {
          alert(await response.text());
        }
      } catch (error) {
        console.error("Error uploading file:", error);
        alert(error.message || "Upload failed.");
      } finally {
        if (encryptedUpload) {
          await encryptedUpload.cleanup();
        }
      }
    });
  }

  // GET
  const getForm = document.getElementById("getForm");
  if (getForm) {
    // we slice it to remove the leading '#'
    const hashParam = location.hash.slice(1);
    const splitParams = hashParam.split('.');
    const shareId = splitParams[0];
    const expiresAtToken = splitParams[1];
    const expiresAt = parseInt(expiresAtToken, 36);
    // might be empty
    const passphraseInUrl = splitParams[2];
    const passphraseInput = document.querySelector('input[name="passphrase"]');
    if (passphraseInUrl) {
      passphraseInput.setAttribute('hidden', 'hidden');
      document.querySelector('.toggle-eye').setAttribute('hidden', 'hidden');
      passphraseInput.value = passphraseInUrl;
    }
    const expiresAtEl = document.getElementById('expiresAt');
    expiresAtEl.innerText = `Expires ${formatUnixTimestamp(expiresAt)}`;
    getForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const passphrase = passphraseInput.value;
      if (!passphrase || !shareId) {
        alert("Please provide both a passphrase and ID.");
        return;
      }
      try {
        // Fetch as a Blob so the browser can keep large ciphertexts outside the JS heap.
        const response = await fetch(`/api/v1/part/${shareId}.${expiresAtToken}`);
        if (!response.ok) {
          alert("Failed to download file. Maybe it is expired?");
          return;
        }
        // change the button to show progress
        mkSpin(getForm.querySelector('button[type="submit"]'));

        try {
          const encryptedBlob = await response.blob();
          const { metadata, file: decryptedFile } = await partage.decryptBlob(encryptedBlob, passphrase);
          document.getElementById('getForm').remove();
          if (metadata.text) {
            const textDiv = document.getElementById('text-div');
            textDiv.removeAttribute('hidden');
            const textDivContent = textDiv.querySelector('p.text');
            textDivContent.innerText = metadata.text;
            // copy to clipboard button
            const copyBtn = document.getElementById('copyBtn');
            copyBtn.addEventListener('click', async () => {
              await navigator.clipboard.writeText(metadata.text);
              copyBtn.innerText = 'Copied to clipboard!';
              setTimeout(() => {
                copyBtn.innerText = 'Copy to clipboard';
              }, 2000);
            });
          }

          if (metadata.filename) {
            document.getElementById('filename').innerText = metadata.filename;
            document.getElementById('filesize').innerText = formatSize(metadata.size);

            const downloadBtn = document.getElementById('downloadBtn');
            downloadBtn.addEventListener('click', () => {
              const link = document.createElement("a");
              const objectUrl = URL.createObjectURL(decryptedFile);
              link.href = objectUrl;
              link.download = metadata.filename;
              document.body.appendChild(link);
              link.click();
              document.body.removeChild(link);
              setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
            });
            downloadDiv.removeAttribute('hidden');
          }
        } catch (err) {
          const errorText = document.getElementById('error');
          if (err instanceof DOMException && err.name === "OperationError") {
            errorText.innerText = "Invalid passphrase or corrupted data.";
          } else {
            errorText.innerText = "Invalid or corrupted encrypted file.";
            console.error(err);
          }
          errorDialog.showModal();
        }
      } catch (err) {
        console.error(err);
      }
    });
  }
});
