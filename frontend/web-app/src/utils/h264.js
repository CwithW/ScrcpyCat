// PREV 使用 Annex B；各播放入口共用分帧与裁剪逻辑。
export function parseAnnexB(buffer) {
  const units = []
  let start = -1
  for (let i = 0; i + 2 < buffer.length; i++) {
    if (buffer[i] !== 0 || buffer[i + 1] !== 0) continue
    const size = buffer[i + 2] === 1 ? 3 : (buffer[i + 2] === 0 && buffer[i + 3] === 1 ? 4 : 0)
    if (!size) continue
    if (start >= 0 && i > start) units.push(buffer.subarray(start, i))
    start = i + size
    i = start - 1
  }
  if (start >= 0 && start < buffer.length) units.push(buffer.subarray(start))
  return units
}

// WASM 返回宏块对齐的 YUV 尺寸，需按 Baseline SPS 裁掉编码填充。
export function readH264Crop(nalu) {
  if ((nalu[0] & 31) !== 7 || ![66, 77, 88].includes(nalu[1])) return null
  const bytes = []
  for (let i = 1; i < nalu.length; i++) {
    if (i > 2 && nalu[i] === 3 && nalu[i - 1] === 0 && nalu[i - 2] === 0) continue
    bytes.push(nalu[i])
  }
  let bit = 0
  const read = count => {
    if (bit + count > bytes.length * 8) throw new Error('Incomplete SPS')
    let result = 0
    for (let i = 0; i < count; i++, bit++) result = result * 2 + ((bytes[bit >> 3] >> (7 - (bit & 7))) & 1)
    return result
  }
  const ue = () => {
    let zeros = 0
    while (read(1) === 0) if (++zeros > 30) throw new Error('Invalid SPS')
    return 2 ** zeros - 1 + read(zeros)
  }
  try {
    read(24)
    ue() // seq_parameter_set_id
    ue() // log2_max_frame_num_minus4
    const order = ue()
    if (order === 0) ue()
    else if (order === 1) {
      read(1); ue(); ue()
      const count = ue()
      if (count > 255) return null
      for (let i = 0; i < count; i++) ue()
    } else if (order !== 2) return null
    ue(); read(1)
    const codedWidth = (ue() + 1) * 16
    const heightUnits = ue() + 1
    const frameOnly = read(1)
    if (!frameOnly) read(1)
    read(1)
    const codedHeight = heightUnits * (2 - frameOnly) * 16
    let left = 0, right = 0, top = 0, bottom = 0
    if (read(1)) { left = ue() * 2; right = ue() * 2; top = ue() * 2 * (2 - frameOnly); bottom = ue() * 2 * (2 - frameOnly) }
    const width = codedWidth - left - right, height = codedHeight - top - bottom
    if (width <= 0 || height <= 0 || codedWidth > 8192 || codedHeight > 8192) return null
    return { codedWidth, codedHeight, width, height, left, top }
  } catch { return null }
}

export function drawYUV420(canvas, yuv, codedWidth, codedHeight, crop) {
  const { width, height, left, top } = crop?.codedWidth === codedWidth && crop?.codedHeight === codedHeight
    ? crop : { width: codedWidth, height: codedHeight, left: 0, top: 0 }
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  if (canvas.width !== width || canvas.height !== height) { canvas.width = width; canvas.height = height }
  const image = ctx.createImageData(width, height)
  const ySize = codedWidth * codedHeight, chromaSize = ySize >> 2
  let offset = 0
  for (let y = 0; y < height; y++) {
    const row = (y + top) * codedWidth, uvRow = ((y + top) >> 1) * (codedWidth >> 1)
    for (let x = 0; x < width; x++) {
      const Y = yuv[row + x + left], uv = uvRow + ((x + left) >> 1)
      const U = yuv[ySize + uv] - 128, V = yuv[ySize + chromaSize + uv] - 128
      image.data[offset++] = Y + 1.402 * V
      image.data[offset++] = Y - 0.344 * U - 0.714 * V
      image.data[offset++] = Y + 1.772 * U
      image.data[offset++] = 255
    }
  }
  ctx.putImageData(image, 0, 0)
  return { width, height }
}
