import assert from 'node:assert/strict';
import test from 'node:test';

import { Partage } from './partage.js';

function makeTestFile(size) {
  const data = new Uint8Array(size);
  for (let i = 0; i < data.length; i++) {
    data[i] = i % 251;
  }
  return {
    data,
    file: new File([data], 'test.bin', { type: 'application/octet-stream' }),
  };
}

test('chunked encryption round trips multiple chunks', async () => {
  const partage = new Partage();
  partage.chunkSize = partage.minChunkSize;
  partage.opfsThreshold = Number.MAX_SAFE_INTEGER;
  const { data, file } = makeTestFile((partage.chunkSize * 2) + 123);

  const encryptedUpload = await partage.getEncryptedBlob(file, 'hello', 'correct horse battery staple');
  try {
    const magic = new Uint8Array(await encryptedUpload.blob.slice(0, partage.formatMagic.length).arrayBuffer());
    assert.deepEqual(magic, partage.formatMagic);

    const decrypted = await new Partage().decryptBlob(encryptedUpload.blob, 'correct horse battery staple');
    assert.equal(decrypted.metadata.filename, 'test.bin');
    assert.equal(decrypted.metadata.size, data.length);
    assert.equal(decrypted.metadata.text, 'hello');
    assert.deepEqual(new Uint8Array(await decrypted.file.arrayBuffer()), data);
  } finally {
    await encryptedUpload.cleanup();
  }
});

test('chunk authentication rejects corrupted ciphertext', async () => {
  const partage = new Partage();
  partage.chunkSize = partage.minChunkSize;
  partage.opfsThreshold = Number.MAX_SAFE_INTEGER;
  const { file } = makeTestFile(partage.chunkSize + 123);

  const encryptedUpload = await partage.getEncryptedBlob(file, '', 'passphrase');
  try {
    const corrupted = new Uint8Array(await encryptedUpload.blob.arrayBuffer());
    corrupted[corrupted.length - 1] ^= 0xff;
    await assert.rejects(
      () => new Partage().decryptBlob(new Blob([corrupted]), 'passphrase'),
      err => err instanceof DOMException && err.name === 'OperationError',
    );
  } finally {
    await encryptedUpload.cleanup();
  }
});

test('non-chunked ciphertext is rejected', async () => {
  const partage = new Partage();
  const invalid = new Blob([new Uint8Array(partage.formatHeaderLength + partage.gcmTagLength)]);
  await assert.rejects(
    () => partage.decryptBlob(invalid, 'passphrase'),
    /Invalid encrypted file format/,
  );
});

test('large encryption stages ciphertext in OPFS and cleans it up', async t => {
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  const parts = [];
  let removed = false;
  let writeCount = 0;

  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      storage: {
        estimate: async () => ({ quota: 1024 * 1024 * 1024, usage: 0 }),
        getDirectory: async () => ({
          getFileHandle: async () => ({
            createWritable: async () => ({
              write: async data => {
                writeCount++;
                parts.push(new Blob([data]));
              },
              close: async () => {},
              abort: async () => {},
            }),
            getFile: async () => new File(parts, 'encrypted.tmp'),
          }),
          removeEntry: async () => {
            removed = true;
          },
        }),
      },
    },
  });
  t.after(() => Object.defineProperty(globalThis, 'navigator', originalNavigator));

  const partage = new Partage();
  partage.chunkSize = partage.minChunkSize;
  partage.opfsThreshold = 1;
  const { data, file } = makeTestFile(partage.chunkSize + 123);
  const encryptedUpload = await partage.getEncryptedBlob(file, '', 'passphrase');

  assert.ok(writeCount >= 4, `expected multiple OPFS writes, got ${writeCount}`);
  assert.equal(removed, false);
  const decrypted = await new Partage().decryptBlob(encryptedUpload.blob, 'passphrase');
  assert.deepEqual(new Uint8Array(await decrypted.file.arrayBuffer()), data);

  await encryptedUpload.cleanup();
  assert.equal(removed, true);
});
