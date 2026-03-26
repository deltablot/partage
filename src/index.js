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

      // do the work
      const encryptedBlob = await partage.getEncryptedBlob(file, text, passphrase);

      // send the encrypted data for storage
      const formData = new FormData();
      // send the key in a custom header
      const partageKey = document.getElementById('x-partage-key').innerText;
      const deadline = document.querySelector('select[name="deadline"]').value;
      const headers = new Headers({'X-Partage-Key': partageKey});
      formData.append("file", encryptedBlob, "partage");
      formData.append("deadline", deadline);
      try {
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
          status.removeAttribute('hidden');
          btnWrapper.removeAttribute('hidden');
          const link = document.createElement('input');
          const linkUrl = `${document.location}get#${json.id}.${json.expires_at}${passphraseInUrl}`;
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
          btnWrapper.appendChild(btn);
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
        alert("Upload failed.");
      }
    });
  }

  // GET
  const getForm = document.getElementById("getForm");
  if (getForm) {
    // we slice it to remove the leading '#'
    const hashParam = location.hash.slice(1);
    const splitParams = hashParam.split('.');
    const uuid = splitParams[0];
    const expiresAt = splitParams[1];
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
      if (!passphrase || !uuid) {
        alert("Please provide both a passphrase and UUID.");
        return;
      }
      try {
        // Fetch the encrypted file as an ArrayBuffer.
        const response = await fetch(`/api/v1/part/${uuid}.${expiresAt}`);
        if (!response.ok) {
          alert("Failed to download file. Maybe it is expired?");
          return;
        }
        // change the button to show progress
        mkSpin(getForm.querySelector('button[type="submit"]'));
        const encryptedDataBuffer = await response.arrayBuffer();
        const dataView = new Uint8Array(encryptedDataBuffer);

        // Check that we have at least 16 bytes salt + 12 bytes IV.
        if (dataView.length < partage.saltLength + partage.ivLength) {
          alert("Invalid encrypted file format.");
          return;
        }
        // Extract the salt (first 16 bytes), IV (next 12 bytes), and ciphertext (rest).
        const salt = dataView.slice(0, partage.saltLength);
        const saltIvLength = partage.saltLength + partage.ivLength
        const iv = dataView.slice(partage.saltLength, saltIvLength);
        const ciphertext = dataView.slice(saltIvLength);

        // Derive the decryption key using PBKDF2 from the passphrase.
        const baseKey = await partage.getBaseKey(passphrase);
        const derivedKey = await partage.getDerivedKey(baseKey, ['decrypt'], salt);
        // Decrypt
        try {
          const decryptedBuffer = await partage.decrypt(derivedKey, iv, ciphertext);
          const clearView = new Uint8Array(decryptedBuffer);

          const header = clearView.slice(0, 2);
          const dv = new DataView(header.buffer);
          const metadataLength = dv.getUint16(0, false);
          const metadataEncoded = clearView.slice(2, metadataLength + 2);
          const metadata = JSON.parse(new TextDecoder().decode(metadataEncoded));
          document.getElementById('getForm').remove();
          if (metadata.text) {
            const textDiv = document.getElementById('textDiv');
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
              const file = clearView.slice(metadataLength + 2);
              // Create a Blob from the decrypted data and trigger a download.
              const blob = new Blob([file], { type: metadata.content_type });
              const link = document.createElement("a");
              link.href = URL.createObjectURL(blob);
              link.download = metadata.filename;
              document.body.appendChild(link);
              link.click();
              document.body.removeChild(link);
            });
            downloadDiv.removeAttribute('hidden');
          }
        } catch (err) {
          // WebCrypto failures in decrypt() all come back as a DOMException
          if (err instanceof DOMException && err.name === "OperationError") {
            // most likely a bad passphrase / auth tag mismatch
            const errorText = document.getElementById('error');
            errorText.innerText = "Invalid passphrase or corrupted data.";
            errorDialog.showModal();
          }
          // re‑throw anything else
          throw err;
        }
      } catch (err) {
        console.error(err);
      }
    });
  }
});
