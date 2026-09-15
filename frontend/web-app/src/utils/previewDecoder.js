import { H264Decoder } from 'h264decoder'
import { parseAnnexB, h264CodecString, readH264Crop, drawYUV420 } from './h264.js'

// 卡片与高密列表共用解码器。配置取自实际 SPS，兼容编码器自行选择的 profile。
export class PreviewDecoder {
  constructor(canvas, { mode = 'wasm', onFrame = () => {}, onError = () => {} } = {}) {
    this.canvas = canvas
    this.mode = mode
    this.onFrame = onFrame
    this.onError = onError
    this.decoder = null
    this.wasm = null
    this.codec = null
    this.keyframe = false
    this.crop = null
    this.closed = false
  }

  feed(data, key, timestamp) {
    if (this.closed || (!this.keyframe && !key)) return
    this.keyframe = true
    const sps = parseAnnexB(data).find(nalu => (nalu[0] & 31) === 7)
    if (sps) this.crop = readH264Crop(sps)
    const codec = h264CodecString(sps) || this.codec
    if (codec) this.codec = codec
    // TinyH264 的软件路径仅支持 Baseline；其他 profile 使用浏览器解码器。
    const useHardware = typeof VideoDecoder !== 'undefined' && (this.mode === 'webcodecs' || (sps && sps[1] !== 66) || this.decoder)
    try {
      if (useHardware) {
        if (!this.decoder || this.decoder.state === 'closed') {
          this.decoder = new VideoDecoder({
            output: frame => {
              try {
                const canvas = this.canvas()
                if (!canvas || this.closed) return
                canvas.width = frame.displayWidth
                canvas.height = frame.displayHeight
                canvas.getContext('2d').drawImage(frame, 0, 0)
                this.onFrame()
              } finally { frame.close() }
            },
            error: error => this.onError(error)
          })
        }
        if (!codec) return
        if (this.decoder.state !== 'configured' || this.configuredCodec !== codec) {
          this.decoder.configure({ codec, optimizeForLatency: true })
          this.configuredCodec = codec
        }
        if (this.decoder.decodeQueueSize > 3 && !key) {
          this.keyframe = false
          return
        }
        this.decoder.decode(new EncodedVideoChunk({ type: key ? 'key' : 'delta', timestamp, data }))
      } else {
        if (sps && sps[1] !== 66) throw new Error('此编码 profile 需要支持 WebCodecs 的浏览器')
        this.wasm ??= new H264Decoder()
        // 完整访问单元保留 SPS/PPS 与图像切片；此库对单独的参数 NAL 返回 ERROR。
        if (data.length > 1024 * 1024) throw new Error('预览帧过大，请降低预览分辨率或使用 WebCodecs')
        const result = this.wasm.decode(data)
        if (result === H264Decoder.PIC_RDY) {
          const canvas = this.canvas()
          if (canvas) {
            drawYUV420(canvas, this.wasm.pic, this.wasm.width, this.wasm.height, this.crop)
            this.onFrame()
          }
        } else if (result >= H264Decoder.ERROR) {
          throw new Error('预览解码失败，请切换 WebCodecs 解码')
        }
      }
    } catch (error) { this.onError(error) }
  }

  close() {
    this.closed = true
    if (this.decoder?.state !== 'closed') this.decoder?.close()
    this.decoder = null
    this.wasm = null
  }
}
