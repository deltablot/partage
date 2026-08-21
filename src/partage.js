export class Partage {
  // AES with Galois/Counter Mode
  encryptionAlgo = 'AES-GCM';
  saltLength = 16;
  ivLength = 12;
  ivPrefixLength = 8;
  gcmTagLength = 16;
  keyDerivationFunction = 'PBKDF2';
  keyDerivationHashAlgo = 'SHA-256';
  iterations = 100000;
  chunkSize = 4 * 1024 * 1024;
  minChunkSize = 64 * 1024;
  maxChunkSize = 16 * 1024 * 1024;
  opfsThreshold = 32 * 1024 * 1024;
  maxMetadataCiphertextLength = 16 * 1024 * 1024;
  formatMagic = new Uint8Array([0x50, 0x41, 0x52, 0x54, 0x41, 0x47, 0x45, 0x32]); // PARTAGE2
  formatHeaderLength = this.formatMagic.length + this.saltLength + this.ivPrefixLength + 4 + 4;

  constructor() {
    this.encoder = new TextEncoder();
    this.decoder = new TextDecoder();
  }

  getMetadata(file, text, fileSize) {
    return {
      content_type: file.type,
      created_at: new Date().toISOString(),
      filename: file.name,
      size: fileSize,
      text: text,
    };
  }

  async getEncryptedBlob(file, text, passphrase) {
    const fileSize = Number.isSafeInteger(file.size) ? file.size : 0;
    const salt = crypto.getRandomValues(new Uint8Array(this.saltLength));
    const ivPrefix = crypto.getRandomValues(new Uint8Array(this.ivPrefixLength));
    const baseKey = await this.getBaseKey(passphrase);
    const derivedKey = await this.getDerivedKey(baseKey, ['encrypt'], salt);

    const metadata = this.getMetadata(file, text, fileSize);
    const metadataEncoded = this.encoder.encode(JSON.stringify(metadata));
    const metadataCiphertextLength = metadataEncoded.byteLength + this.gcmTagLength;
    if (metadataCiphertextLength > this.maxMetadataCiphertextLength) {
      throw new Error('Metadata is too large.');
    }

    const header = this.createFormatHeader(salt, ivPrefix, this.chunkSize, metadataCiphertextLength);
    const encryptedSize = this.getEncryptedSize(fileSize, metadataCiphertextLength, this.chunkSize);
    const sink = await this.createCiphertextSink(encryptedSize);

    try {
      await sink.write(header);
      await sink.write(await this.encryptChunk(derivedKey, ivPrefix, 0, metadataEncoded, header));

      let chunkIndex = 1;
      for (let offset = 0; offset < fileSize; offset += this.chunkSize) {
        const chunk = await file.slice(offset, Math.min(offset + this.chunkSize, fileSize)).arrayBuffer();
        await sink.write(await this.encryptChunk(derivedKey, ivPrefix, chunkIndex, chunk, header));
        chunkIndex++;
      }

      return {
        blob: await sink.finish(),
        cleanup: sink.cleanup,
      };
    } catch (err) {
      await sink.abort();
      throw err;
    }
  }

  getEncryptedSize(fileSize, metadataCiphertextLength, chunkSize) {
    const chunkCount = Math.ceil(fileSize / chunkSize);
    return this.formatHeaderLength + metadataCiphertextLength + fileSize + (chunkCount * this.gcmTagLength);
  }

  createFormatHeader(salt, ivPrefix, chunkSize, metadataCiphertextLength) {
    const header = new Uint8Array(this.formatHeaderLength);
    let offset = 0;
    header.set(this.formatMagic, offset);
    offset += this.formatMagic.length;
    header.set(salt, offset);
    offset += this.saltLength;
    header.set(ivPrefix, offset);
    offset += this.ivPrefixLength;
    const view = new DataView(header.buffer);
    view.setUint32(offset, chunkSize, false);
    offset += 4;
    view.setUint32(offset, metadataCiphertextLength, false);
    return header;
  }

  async createCiphertextSink(encryptedSize) {
    if (encryptedSize <= this.opfsThreshold) {
      return this.createMemorySink();
    }
    if (typeof navigator === 'undefined' || !navigator.storage?.getDirectory) {
      throw new Error('Browser disk storage is unavailable for this large upload.');
    }

    const estimate = await navigator.storage.estimate();
    const available = estimate.quota - estimate.usage;
    if (Number.isFinite(available) && available < encryptedSize) {
      throw new Error('Not enough temporary browser storage for this large upload.');
    }

    try {
      return await this.createOpfsSink();
    } catch (err) {
      throw new Error('Unable to create temporary browser storage for this large upload.', { cause: err });
    }
  }

  createMemorySink() {
    const parts = [];
    return {
      write: async data => {
        parts.push(new Blob([data]));
      },
      finish: async () => new Blob(parts, { type: 'application/octet-stream' }),
      cleanup: async () => {},
      abort: async () => {},
    };
  }

  async createOpfsSink() {
    const root = await navigator.storage.getDirectory();
    const filename = `.partage-${crypto.randomUUID()}`;
    const fileHandle = await root.getFileHandle(filename, { create: true });
    const writable = await fileHandle.createWritable();
    let closed = false;

    const cleanup = async () => {
      try {
        await root.removeEntry(filename);
      } catch (err) {
        if (err.name !== 'NotFoundError') {
          console.warn('Unable to remove temporary encrypted file.', err);
        }
      }
    };

    return {
      write: async data => {
        await writable.write(data);
      },
      finish: async () => {
        await writable.close();
        closed = true;
        return fileHandle.getFile();
      },
      cleanup,
      abort: async () => {
        if (!closed) {
          try {
            await writable.abort();
          } catch {
            // Ignore cleanup errors after a failed encryption.
          }
        }
        await cleanup();
      },
    };
  }

  createChunkIv(ivPrefix, chunkIndex) {
    if (!Number.isSafeInteger(chunkIndex) || chunkIndex < 0 || chunkIndex > 0xffffffff) {
      throw new Error('File has too many chunks.');
    }
    const iv = new Uint8Array(this.ivLength);
    iv.set(ivPrefix, 0);
    new DataView(iv.buffer).setUint32(this.ivPrefixLength, chunkIndex, false);
    return iv;
  }

  async encryptChunk(key, ivPrefix, chunkIndex, data, additionalData) {
    return crypto.subtle.encrypt(
      {
        name: this.encryptionAlgo,
        iv: this.createChunkIv(ivPrefix, chunkIndex),
        additionalData,
        tagLength: 128,
      },
      key,
      data,
    );
  }

  async decryptChunk(key, ivPrefix, chunkIndex, data, additionalData) {
    return crypto.subtle.decrypt(
      {
        name: this.encryptionAlgo,
        iv: this.createChunkIv(ivPrefix, chunkIndex),
        additionalData,
        tagLength: 128,
      },
      key,
      data,
    );
  }

  async decryptBlob(encryptedBlob, passphrase) {
    if (encryptedBlob.size < this.formatHeaderLength + this.gcmTagLength) {
      throw new Error('Invalid encrypted file format.');
    }

    const header = new Uint8Array(await encryptedBlob.slice(0, this.formatHeaderLength).arrayBuffer());
    if (!this.arraysEqual(header.slice(0, this.formatMagic.length), this.formatMagic)) {
      throw new Error('Invalid encrypted file format.');
    }

    let offset = this.formatMagic.length;
    const salt = header.slice(offset, offset + this.saltLength);
    offset += this.saltLength;
    const ivPrefix = header.slice(offset, offset + this.ivPrefixLength);
    offset += this.ivPrefixLength;
    const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
    const chunkSize = view.getUint32(offset, false);
    offset += 4;
    const metadataCiphertextLength = view.getUint32(offset, false);

    if (chunkSize < this.minChunkSize || chunkSize > this.maxChunkSize) {
      throw new Error('Invalid encrypted file format.');
    }
    if (metadataCiphertextLength < this.gcmTagLength || metadataCiphertextLength > this.maxMetadataCiphertextLength) {
      throw new Error('Invalid encrypted file format.');
    }

    const metadataEnd = this.formatHeaderLength + metadataCiphertextLength;
    if (metadataEnd > encryptedBlob.size) {
      throw new Error('Invalid encrypted file format.');
    }

    const baseKey = await this.getBaseKey(passphrase);
    const derivedKey = await this.getDerivedKey(baseKey, ['decrypt'], salt);
    const encryptedMetadata = await encryptedBlob.slice(this.formatHeaderLength, metadataEnd).arrayBuffer();
    const metadataBuffer = await this.decryptChunk(derivedKey, ivPrefix, 0, encryptedMetadata, header);
    const metadata = JSON.parse(this.decoder.decode(metadataBuffer));

    if (!Number.isSafeInteger(metadata.size) || metadata.size < 0) {
      throw new Error('Invalid encrypted file format.');
    }

    const fileParts = [];
    let ciphertextOffset = metadataEnd;
    let plaintextRemaining = metadata.size;
    let chunkIndex = 1;

    while (plaintextRemaining > 0) {
      const plaintextLength = Math.min(chunkSize, plaintextRemaining);
      const ciphertextLength = plaintextLength + this.gcmTagLength;
      const ciphertextEnd = ciphertextOffset + ciphertextLength;
      if (ciphertextEnd > encryptedBlob.size) {
        throw new Error('Invalid encrypted file format.');
      }

      const encryptedChunk = await encryptedBlob.slice(ciphertextOffset, ciphertextEnd).arrayBuffer();
      const decryptedChunk = await this.decryptChunk(derivedKey, ivPrefix, chunkIndex, encryptedChunk, header);
      if (decryptedChunk.byteLength !== plaintextLength) {
        throw new Error('Invalid encrypted file format.');
      }
      fileParts.push(new Blob([decryptedChunk]));

      ciphertextOffset = ciphertextEnd;
      plaintextRemaining -= plaintextLength;
      chunkIndex++;
    }

    if (ciphertextOffset !== encryptedBlob.size) {
      throw new Error('Invalid encrypted file format.');
    }

    return {
      metadata,
      file: new Blob(fileParts, { type: metadata.content_type || 'application/octet-stream' }),
    };
  }

  arraysEqual(left, right) {
    if (left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => value === right[index]);
  }

  async getBaseKey(passphrase) {
    const passphraseBuffer = this.encoder.encode(passphrase);
    return crypto.subtle.importKey(
      'raw',
      passphraseBuffer,
      this.keyDerivationFunction,
      false,
      ['deriveKey']
    );
  }

  async getDerivedKey(baseKey, keyUsages, salt) {
    return crypto.subtle.deriveKey(
      {
        name: this.keyDerivationFunction,
        salt,
        iterations: this.iterations,
        hash: this.keyDerivationHashAlgo,
      },
      baseKey,
      { name: this.encryptionAlgo, length: 256 },
      false,
      keyUsages
    );
  }

}
