// 局域网 HTTP (非 Secure Context) SubtleCrypto 垫片
// 如果浏览器禁用了 crypto.subtle（例如 http://192.168.x.x 局域网访问），
// 我们注入一个用纯 BigInt + 硬编码 DER 编码实现的 SubtleCrypto 垫片。
if (typeof window !== 'undefined') {
  if (!window.crypto) {
    window.crypto = {};
  }
  if (!window.crypto.subtle) {
    console.warn('[ADB] HTTP non-Secure Context detected. Polyfilling window.crypto.subtle globally for ADB Auth...')

    const fallbackN = BigInt('0xc0b8574a9dec3aa300c15fd367b90b50182ca2a6072bb9ae1128e552571d543d245bc4063c778ffb39ac57620241baeb409844034d28a9bb118bcc1750bce8c501b7b7df93398aaa33dbe8fc1a927d62e33c6afc690b976fdb6e5aaef0e70f4444bce6bf567ffb6d857e8296597e8be22626a948d71303c5d6388745b418bc6aa9952b4b64d6e2787a250ab5a2cc69c6c1b343e972445c1eda63117b48d01c38e6c93c7efb4549143fdce4077dc5c14898a9432a09b66d68eb116ad7fcdee7dc50f78b5f3c18429beac36fff5c5d310184dec698c0dc941c7a71fd55b60a79df547b04669cbe8517208cf3a25aeeea8851de7226bfd97d524bfb5e0c9d4e0b81')
    const fallbackD = BigInt('0x18ec7e4039d64aeb0e76491f526dbdb1ce693a0bc0d2532c2e2f477673aa69079343d99678e17999cda6a53d194f9e3893325e06bdf181549e7b4c44c3a8a56de7bff67965554417fe51a025b7db5ecded8215d003ac7fc44946520207f3c9fca940739326965ca79863ce730555f6b4a3a72258e23fcfa0195eac2d43e7b8637faee9396abbfc9916269b50b7e044d2b154726ae67e721d228fdeb2f69dafc37a26026f70c18803d6320f89bf622d970b723fe430c2a8fa7685254a7cd61ef4b2319be47c4dd492cfa2945ca4e0e9881df5a9302317dd36c67c539d5d712413c9dd56f05867070a1a18cef83f9f450e8556f6a7a85b38daa5f5b6ec08f4b215')

    // 固定的 DER Hex 转换回 Uint8Array
    const hexToBytes = (hex) => {
      const bytes = new Uint8Array(hex.length / 2)
      for (let i = 0; i < bytes.length; i++) {
        bytes[i] = parseInt(hex.substr(i * 2, 2), 16)
      }
      return bytes
    }

    const spkiDer = hexToBytes('30820122300d06092a864886f70d01010105000382010f003082010a0282010100c0b8574a9dec3aa300c15fd367b90b50182ca2a6072bb9ae1128e552571d543d245bc4063c778ffb39ac57620241baeb409844034d28a9bb118bcc1750bce8c501b7b7df93398aaa33dbe8fc1a927d62e33c6afc690b976fdb6e5aaef0e70f4444bce6bf567ffb6d857e8296597e8be22626a948d71303c5d6388745b418bc6aa9952b4b64d6e2787a250ab5a2cc69c6c1b343e972445c1eda63117b48d01c38e6c93c7efb4549143fdce4077dc5c14898a9432a09b66d68eb116ad7fcdee7dc50f78b5f3c18429beac36fff5c5d310184dec698c0dc941c7a71fd55b60a79df547b04669cbe8517208cf3a25aeeea8851de7226bfd97d524bfb5e0c9d4e0b810203010001')
    const pkcs8Der = hexToBytes('308202170201000282010100c0b8574a9dec3aa300c15fd367b90b50182ca2a6072bb9ae1128e552571d543d245bc4063c778ffb39ac57620241baeb409844034d28a9bb118bcc1750bce8c501b7b7df93398aaa33dbe8fc1a927d62e33c6afc690b976fdb6e5aaef0e70f4444bce6bf567ffb6d857e8296597e8be22626a948d71303c5d6388745b418bc6aa9952b4b64d6e2787a250ab5a2cc69c6c1b343e972445c1eda63117b48d01c38e6c93c7efb4549143fdce4077dc5c14898a9432a09b66d68eb116ad7fcdee7dc50f78b5f3c18429beac36fff5c5d310184dec698c0dc941c7a71fd55b60a79df547b04669cbe8517208cf3a25aeeea8851de7226bfd97d524bfb5e0c9d4e0b8102030100010282010018ec7e4039d64aeb0e76491f526dbdb1ce693a0bc0d2532c2e2f477673aa69079343d99678e17999cda6a53d194f9e3893325e06bdf181549e7b4c44c3a8a56de7bff67965554417fe51a025b7db5ecded8215d003ac7fc44946520207f3c9fca940739326965ca79863ce730555f6b4a3a72258e23fcfa0195eac2d43e7b8637faee9396abbfc9916269b50b7e044d2b154726ae67e721d228fdeb2f69dafc37a26026f70c18803d6320f89bf622d970b723fe430c2a8fa7685254a7cd61ef4b2319be47c4dd492cfa2945ca4e0e9881df5a9302317dd36c67c539d5d712413c9dd56f05867070a1a18cef83f9f450e8556f6a7a85b38daa5f5b6ec08f4b215020100020100')

    // 快速幂实现 M^d mod N
    const powerMod = (base, exp, mod) => {
      let res = 1n
      base = base % mod
      while (exp > 0n) {
        if (exp % 2n === 1n) res = (res * base) % mod
        base = (base * base) % mod
        exp = exp / 2n
      }
      return res
    }

    // 大数转换为 Uint8Array (固定 256 字节长度)
    const bigintToBytes = (num) => {
      const bytes = new Uint8Array(256)
      let temp = num
      for (let i = 255; i >= 0; i--) {
        bytes[i] = Number(temp & 0xffn)
        temp = temp >> 8n
      }
      return bytes
    }

    // 自适应 PKCS#1 v1.5 填充并加密 Token 挑战数据
    const signAdbToken = (tokenBytes) => {
      const len = tokenBytes.length
      const padded = new Uint8Array(256)
      padded[0] = 0x00
      padded[1] = 0x01
      const padLength = 256 - 3 - len
      for (let i = 0; i < padLength; i++) {
        padded[2 + i] = 0xff
      }
      padded[2 + padLength] = 0x00
      padded.set(tokenBytes, 2 + padLength + 1)

      // 转换为 BigInt
      let m = 0n
      for (let i = 0; i < 256; i++) {
        m = (m << 8n) + BigInt(padded[i])
      }

      const s = powerMod(m, fallbackD, fallbackN)
      return bigintToBytes(s)
    }

    const polyfillSubtle = {
      async generateKey(algorithm, extractable, keyUsages) {
        return {
          privateKey: { type: 'private', algorithm, extractable, usages: keyUsages, _cloudphoneFallback: true },
          publicKey: { type: 'public', algorithm, extractable, usages: keyUsages, _cloudphoneFallback: true }
        }
      },
      async exportKey(format, key) {
        if (key.type === 'private') {
          return pkcs8Der.buffer
        }
        return spkiDer.buffer
      },
      async sign(algorithm, key, data) {
        if (key._cloudphoneFallback) {
          return signAdbToken(new Uint8Array(data)).buffer
        }
        throw new Error('Unsupported key for fallback signature')
      }
    }

    window.crypto.subtle = polyfillSubtle;
  }
}
