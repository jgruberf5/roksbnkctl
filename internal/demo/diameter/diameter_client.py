#!/usr/bin/env python3
"""Minimal Diameter base-protocol (RFC 6733) client — stdlib only (3.9+).

Connects to <host> <port> (default 3868), sends a Capabilities-Exchange-Request
(CER), reads the answer, and validates it is a Capabilities-Exchange-Answer
(CEA, command 257, Answer) with Result-Code 2001 (DIAMETER_SUCCESS).

Prints PASS + parsed Result-Code/Origin-Host and exits 0 on success; otherwise
prints FAIL and exits non-zero. The wire codec is byte-for-byte identical to
responder.py (kept in sync deliberately).

Usage:
    python3 diameter_client.py <host> [port]
"""

import socket
import struct
import sys

# --- Diameter constants (RFC 6733) ---------------------------------------

CMD_CER_CEA = 257
APP_ID_BASE = 0

FLAG_REQUEST = 0x80
FLAG_PROXIABLE = 0x40
FLAG_ERROR = 0x20
FLAG_RETRANSMIT = 0x10

AVP_HOST_IP_ADDRESS = 257
AVP_AUTH_APPLICATION_ID = 258
AVP_ORIGIN_HOST = 264
AVP_VENDOR_ID = 266
AVP_RESULT_CODE = 268
AVP_PRODUCT_NAME = 269
AVP_ORIGIN_REALM = 296

AVP_FLAG_MANDATORY = 0x40
AVP_FLAG_VENDOR = 0x80

RESULT_SUCCESS = 2001
ADDR_FAMILY_IPV4 = 1

ORIGIN_HOST = b"client.awsbnkctl.diameter"
ORIGIN_REALM = b"awsbnkctl.diameter"
PRODUCT_NAME = b"awsbnkctl-diameter-client"


# --- AVP / message codec (identical to responder.py) ----------------------

def _pad4(n):
    return (4 - (n % 4)) % 4


def encode_avp(code, data, mandatory=True, vendor_id=None):
    flags = 0
    if mandatory:
        flags |= AVP_FLAG_MANDATORY
    header_len = 8
    vendor_bytes = b""
    if vendor_id is not None:
        flags |= AVP_FLAG_VENDOR
        vendor_bytes = struct.pack("!I", vendor_id)
        header_len += 4
    avp_len = header_len + len(data)
    out = struct.pack("!I", code)
    out += struct.pack("!B", flags)
    out += struct.pack("!I", avp_len)[1:]
    out += vendor_bytes
    out += data
    out += b"\x00" * _pad4(avp_len)
    return out


def decode_avps(buf):
    avps = {}
    i = 0
    n = len(buf)
    while i + 8 <= n:
        code = struct.unpack("!I", buf[i:i + 4])[0]
        flags = buf[i + 4]
        avp_len = struct.unpack("!I", b"\x00" + buf[i + 5:i + 8])[0]
        if avp_len < 8 or i + avp_len > n:
            break
        data_off = i + 8
        if flags & AVP_FLAG_VENDOR:
            data_off += 4
        data = buf[data_off:i + avp_len]
        avps[code] = data
        i += avp_len + _pad4(avp_len)
    return avps


def encode_message(command_code, app_id, hbh, e2e, avps, request=False,
                   proxiable=False, error=False):
    body = b"".join(avps)
    msg_len = 20 + len(body)
    flags = 0
    if request:
        flags |= FLAG_REQUEST
    if proxiable:
        flags |= FLAG_PROXIABLE
    if error:
        flags |= FLAG_ERROR
    out = struct.pack("!B", 1)
    out += struct.pack("!I", msg_len)[1:]
    out += struct.pack("!B", flags)
    out += struct.pack("!I", command_code)[1:]
    out += struct.pack("!I", app_id)
    out += struct.pack("!I", hbh)
    out += struct.pack("!I", e2e)
    out += body
    return out


def decode_header(buf20):
    version = buf20[0]
    msg_len = struct.unpack("!I", b"\x00" + buf20[1:4])[0]
    flags = buf20[4]
    command = struct.unpack("!I", b"\x00" + buf20[5:8])[0]
    app_id, hbh, e2e = struct.unpack("!III", buf20[8:20])
    return {
        "version": version,
        "msg_len": msg_len,
        "flags": flags,
        "command": command,
        "app_id": app_id,
        "hbh": hbh,
        "e2e": e2e,
    }


def encode_host_ip(ip_str):
    packed = socket.inet_aton(ip_str)
    return struct.pack("!H", ADDR_FAMILY_IPV4) + packed


def recv_exact(conn, n):
    chunks = []
    got = 0
    while got < n:
        chunk = conn.recv(n - got)
        if not chunk:
            return None
        chunks.append(chunk)
        got += len(chunk)
    return b"".join(chunks)


def read_message(conn):
    header = recv_exact(conn, 20)
    if header is None:
        return None
    hdr = decode_header(header)
    body_len = hdr["msg_len"] - 20
    if body_len < 0:
        return None
    body = recv_exact(conn, body_len) if body_len else b""
    if body is None:
        return None
    return header + body


# --- client logic ---------------------------------------------------------

def build_cer(hbh, e2e, local_ip):
    avps = [
        encode_avp(AVP_ORIGIN_HOST, ORIGIN_HOST),
        encode_avp(AVP_ORIGIN_REALM, ORIGIN_REALM),
        encode_avp(AVP_HOST_IP_ADDRESS, encode_host_ip(local_ip)),
        encode_avp(AVP_VENDOR_ID, struct.pack("!I", 0)),
        encode_avp(AVP_PRODUCT_NAME, PRODUCT_NAME, mandatory=False),
        encode_avp(AVP_AUTH_APPLICATION_ID, struct.pack("!I", APP_ID_BASE)),
    ]
    return encode_message(CMD_CER_CEA, APP_ID_BASE, hbh, e2e, avps,
                          request=True)


def main():
    if len(sys.argv) < 2:
        print("usage: diameter_client.py <host> [port]", file=sys.stderr)
        return 2
    host = sys.argv[1]
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 3868

    hbh = 0x11111111
    e2e = 0x22222222

    print(f"[client] connecting to {host}:{port} ...", flush=True)
    try:
        conn = socket.create_connection((host, port), timeout=10)
    except OSError as exc:
        print(f"FAIL: cannot connect to {host}:{port}: {exc}")
        return 1

    try:
        local_ip = conn.getsockname()[0]
        cer = build_cer(hbh, e2e, local_ip)
        conn.sendall(cer)
        print(f"[client] sent CER (cmd=257 R=1 app=0 hbh={hbh} e2e={e2e})",
              flush=True)

        conn.settimeout(10)
        msg = read_message(conn)
        if msg is None:
            print("FAIL: no response (connection closed before a full message)")
            return 1

        hdr = decode_header(msg[:20])
        avps = decode_avps(msg[20:])
        is_answer = not (hdr["flags"] & FLAG_REQUEST)

        result_code = None
        if AVP_RESULT_CODE in avps and len(avps[AVP_RESULT_CODE]) == 4:
            result_code = struct.unpack("!I", avps[AVP_RESULT_CODE])[0]
        origin_host = avps.get(AVP_ORIGIN_HOST, b"").decode(
            "utf-8", "replace")

        print(
            f"[client] response cmd={hdr['command']} answer={int(is_answer)} "
            f"hbh={hdr['hbh']} e2e={hdr['e2e']} "
            f"Result-Code={result_code} Origin-Host={origin_host!r}",
            flush=True,
        )

        ok = (
            hdr["command"] == CMD_CER_CEA
            and is_answer
            and hdr["hbh"] == hbh
            and hdr["e2e"] == e2e
            and result_code == RESULT_SUCCESS
        )
        if ok:
            print(
                f"PASS: received CEA, Result-Code={result_code} "
                f"(DIAMETER_SUCCESS), Origin-Host={origin_host!r}"
            )
            return 0
        print(
            f"FAIL: not a successful CEA "
            f"(command={hdr['command']}, answer={is_answer}, "
            f"Result-Code={result_code})"
        )
        return 1
    finally:
        conn.close()


if __name__ == "__main__":
    sys.exit(main())
