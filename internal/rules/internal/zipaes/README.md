# Trimmed WinZip AES-256 ZIP fork

Forked from github.com/yeka/zip at commit 03d6312748a9 (Go module version v0.0.0-20231116150916-03d6312748a9, https://github.com/yeka/zip/tree/03d6312748a9). This copy is kept only for AES-256 archives in internal/rules and is no longer synced with upstream. Do not use it directly in the application.

LICENSE is the upstream MIT license. LICENSE-GO is the Go Authors BSD license referenced by the files derived from archive/zip. Original copyright notices remain.

## What is kept

NewWriter, Writer.RegisterCompressor, Writer.CreateHeader, Writer.Close, NewReader, File.Open, File.DataOffset, and the FileHeader methods SetModTime, SetPassword, SetEncryptionMethod and IsEncrypted. Store and Deflate are the only compression methods. The reader still parses ZIP64 records, data descriptors and unencrypted members, so the upstream parser tests keep running.

## Changes from upstream

1. Removed: OpenReader, ReadCloser, FileInfoHeader, FileHeader.FileInfo, Mode, SetMode and ModTime, Writer.Create, SetOffset, Flush and Encrypt, the global RegisterCompressor and RegisterDecompressor, legacy ZipCrypto, AES-128, AES-192, FileHeader.DeferAuth and the buffered auth reader, and the upstream tests that covered only those.
2. writer.go: RegisterCompressor is per Writer, so each upload can use its own flate level. A password-protected header without AES256Encryption is rejected.
3. reader.go: an encrypted member without a WinZip AES extra field returns ErrAlgorithm; a member whose AES strength is not 256 returns ErrDecryption.
4. crypto.go: newDecryptionReader reads the whole ciphertext through the HMAC to io.Discard, requires authentication to pass, then rewinds the same SectionReader and decrypts. No plaintext is returned before authentication and the file is not buffered in memory. PBKDF2 uses the standard library crypto/pbkdf2. The PBKDF2, AES, CTR, HMAC and wire formats are unchanged.

## Tests and wrapper safeguards

crypto_test.go covers AES-256 Store and Deflate round trips, tampering that fails during Open, wrong passwords, AE-1 CRC checks, and the rejection of ZipCrypto, AES-128 and AES-192. reader_test.go and zip_test.go keep the upstream malformed-input and ZIP64 tests. internal/rules/zip_aes_test.go adds 7-Zip interoperability, concurrent levels -2..9, empty members, I/O failures and cancellation.

The rules wrapper uses the standard archive/zip parser before this reader, validates AES extra fields and all member and data boundaries, accepts only one regular AES-256 Store or Deflate member, and compares both parsers' results. Do not bypass the wrapper.

The ReaderAt source must be immutable: authenticate and decrypt the same private snapshot handle, never a file that may change between passes. All output stays temporary until the wrapper returns nil. WinZip AES does not authenticate filenames or other archive metadata. There is no fixed file size limit; callers enforce disk quotas. AE-2 omits CRC by specification.

The WinZip PBKDF2-SHA1 iteration count is fixed at 1000 for interoperability, so strong passwords matter. This is not a third-party security audit. Check Go archive/zip security fixes before widening accepted inputs.
