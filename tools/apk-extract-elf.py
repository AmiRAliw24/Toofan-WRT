#!/usr/bin/env python3
"""Extract the first ELF executable from an OpenWrt apk-v3 package.

OpenWrt 25.12+ ships packages in apk v3 ("ADBd" = deflate-compressed ADB).
The file data is stored uncompressed inside the ADB stream, so the ELF can
be located by its magic and trimmed using its own section header table.
Usage: apk-extract-elf.py package.apk output
"""
import struct
import sys
import zlib

data = open(sys.argv[1], "rb").read()
if data[:4] != b"ADBd":
    sys.exit("not a compressed apk v3 package")
adb = zlib.decompressobj(-15).decompress(data[4:])
start = adb.find(b"\x7fELF")
if start < 0:
    sys.exit("no ELF inside")
elf = adb[start:]
is64 = elf[4] == 2
endian = "<" if elf[5] == 1 else ">"
if is64:
    shoff, = struct.unpack_from(endian + "Q", elf, 0x28)
    shentsize, shnum = struct.unpack_from(endian + "HH", elf, 0x3A)
else:
    shoff, = struct.unpack_from(endian + "I", elf, 0x20)
    shentsize, shnum = struct.unpack_from(endian + "HH", elf, 0x2E)
end = shoff + shentsize * shnum
if not 0 < end <= len(elf):
    sys.exit("bad ELF section table")
open(sys.argv[2], "wb").write(elf[:end])
print(f"extracted {end} bytes")
