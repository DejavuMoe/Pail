# Isolated WinZip AES ZIP implementation

Source: github.com/yeka/zip, commit 03d6312748a9, Go module version v0.0.0-20231116150916-03d6312748a9. Source URL: https://github.com/yeka/zip/tree/03d6312748a9 . This copy is private to internal/rules; do not use it directly in the application.

The six production Go files, upstream tests, and testdata are retained. LICENSE is the upstream MIT license. LICENSE-GO retains the Go Authors BSD license referenced by derived source files. Original copyright notices remain. No upstream README graphics or product resources are copied.

## Local production patches

1. writer.go: add a per-Writer compressor map and RegisterCompressor, falling back to the upstream registry. Each encrypted upload can independently use Go flate levels without changing a process-global registry.
2. crypto.go: in newDecryptionReader, eagerly consume the existing streaming auth reader to io.Discard, require successful authentication, rewind the same ciphertext SectionReader, then use the original decryptStream. This replaces file-sized buffering for the default authenticated reader. The original DeferAuth path is retained for upstream compatibility, but the public rules API always disables it. No PBKDF2, AES, CTR, HMAC, or wire-format implementation is changed.

## Tests and wrapper safeguards

crypto_test.go updates tamper expectations to fail during Open, before any plaintext reader is returned; it also covers the upstream deferred path. example_test.go imports this isolated package. Other upstream tests and fixtures are unchanged. rules/zip_aes_test.go adds independent 7-Zip interoperability, concurrent levels -2..9, empty members, wrong passwords, tampering with zero output, bounded two-pass 80MiB input, I/O failures, and cancellation.

The rules wrapper uses the maintained standard ZIP parser before this legacy reader, validates AES extra fields and all member/data boundaries, accepts only one safe regular AES256 Store/Deflate member, and compares parser results. It avoids legacy ZipCrypto, AES128/192, unvalidated malformed extra fields, and directory extraction. Do not bypass the wrapper.

The immutable ReaderAt source contract is essential: authenticate and decrypt the same private snapshot handle, never a file that may change between passes. All output remains temporary until the wrapper returns nil. WinZip AES does not authenticate filenames or other archive metadata. There is no fixed 64MiB file limit; callers must enforce disk quotas. AE-2 omits CRC by specification.

This is inspected upstream code with a deliberately narrow fork, not a third-party security audit. The required WinZip PBKDF2-SHA1 iteration count is only 1000; interoperability preserves it and therefore strong passwords matter. Review upstream and Go ZIP security changes before widening accepted inputs. Keep production patch scope and the documented source version synchronized.
