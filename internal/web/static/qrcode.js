// Minimal QR encoder: byte mode + L (low) error correction, versions
// 1–10. Renders to SVG. Used by the share modal so the offline app
// can produce a scannable code without a CDN dependency.
//
// Spec: ISO/IEC 18004:2015, simplified for our subset.
//   - Byte mode only (UTF-8 input).
//   - Low-level error correction (~7%).
//   - Versions 1–10 (max ~230 ASCII chars).
//   - Mask 0 only (skip the 8-mask penalty search).
//
// Exposes window.qrEncodeSvg(text, scale=4) -> string (SVG markup).

(function (global) {
  // ===== Galois field GF(256) tables =====
  var EXP = new Array(256);
  var LOG = new Array(256);
  (function init() {
    var x = 1;
    for (var i = 0; i < 255; i++) {
      EXP[i] = x;
      LOG[x] = i;
      x <<= 1;
      if (x & 0x100) x ^= 0x11d;
    }
    EXP[255] = EXP[0];
  })();

  function gfMul(a, b) {
    if (a === 0 || b === 0) return 0;
    return EXP[(LOG[a] + LOG[b]) % 255];
  }

  function rsPoly(degree) {
    var poly = [1];
    for (var i = 0; i < degree; i++) {
      var next = new Array(poly.length + 1);
      for (var k = 0; k < next.length; k++) next[k] = 0;
      for (var j = 0; j < poly.length; j++) {
        next[j] ^= poly[j];
        next[j + 1] ^= gfMul(poly[j], EXP[i]);
      }
      poly = next;
    }
    return poly;
  }

  function rsEncode(data, ecCount) {
    var poly = rsPoly(ecCount);
    var buf = data.slice();
    for (var i = 0; i < ecCount; i++) buf.push(0);
    for (var i = 0; i < data.length; i++) {
      var coef = buf[i];
      if (coef !== 0) {
        for (var j = 0; j < poly.length; j++) {
          buf[i + j] ^= gfMul(poly[j], coef);
        }
      }
    }
    return buf.slice(data.length);
  }

  // ===== Version capacity tables (L correction, byte mode) =====
  // {size, ecPerBlock, groups: [[blockCount, dataBytesPerBlock], ...]}
  var VER = [
    { size: 21, ec: 7,  groups: [[1, 19]] },
    { size: 25, ec: 10, groups: [[1, 34]] },
    { size: 29, ec: 15, groups: [[1, 55]] },
    { size: 33, ec: 20, groups: [[1, 80]] },
    { size: 37, ec: 26, groups: [[1, 108]] },
    { size: 41, ec: 18, groups: [[2, 68]] },
    { size: 45, ec: 20, groups: [[2, 78]] },
    { size: 49, ec: 24, groups: [[2, 97]] },
    { size: 53, ec: 30, groups: [[2, 116]] },
    { size: 57, ec: 18, groups: [[2, 68], [2, 69]] }
  ];

  // Alignment-pattern centre coordinates for each version.
  var ALIGN = [
    [], [6, 18], [6, 22], [6, 26], [6, 30],
    [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]
  ];

  function totalDataBytes(info) {
    var t = 0;
    for (var i = 0; i < info.groups.length; i++) {
      t += info.groups[i][0] * info.groups[i][1];
    }
    return t;
  }

  function pickVersion(byteLen) {
    for (var v = 0; v < VER.length; v++) {
      var info = VER[v];
      var ccBits = (v + 1) <= 9 ? 8 : 16;
      var maxBytes = totalDataBytes(info) - 2 - Math.ceil(ccBits / 8);
      if (byteLen <= maxBytes) return v + 1;
    }
    return -1;
  }

  function utf8Bytes(s) {
    var bytes = [];
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c < 0x80) {
        bytes.push(c);
      } else if (c < 0x800) {
        bytes.push(0xc0 | (c >> 6));
        bytes.push(0x80 | (c & 0x3f));
      } else if (c < 0xd800 || c >= 0xe000) {
        bytes.push(0xe0 | (c >> 12));
        bytes.push(0x80 | ((c >> 6) & 0x3f));
        bytes.push(0x80 | (c & 0x3f));
      } else {
        var c2 = s.charCodeAt(++i);
        var cp = 0x10000 + (((c & 0x3ff) << 10) | (c2 & 0x3ff));
        bytes.push(0xf0 | (cp >> 18));
        bytes.push(0x80 | ((cp >> 12) & 0x3f));
        bytes.push(0x80 | ((cp >> 6) & 0x3f));
        bytes.push(0x80 | (cp & 0x3f));
      }
    }
    return bytes;
  }

  function buildBitstream(bytes, version, info) {
    var ccBits = version <= 9 ? 8 : 16;
    var totalBytes = totalDataBytes(info);
    var totalBits = totalBytes * 8;

    var bits = [];
    function pushBits(val, n) {
      for (var i = n - 1; i >= 0; i--) bits.push((val >> i) & 1);
    }
    pushBits(0x4, 4); // byte mode
    pushBits(bytes.length, ccBits);
    for (var i = 0; i < bytes.length; i++) pushBits(bytes[i], 8);

    var term = Math.min(4, totalBits - bits.length);
    for (var i = 0; i < term; i++) bits.push(0);
    while (bits.length % 8 !== 0) bits.push(0);

    var out = [];
    for (var i = 0; i < bits.length; i += 8) {
      var b = 0;
      for (var j = 0; j < 8; j++) b = (b << 1) | bits[i + j];
      out.push(b);
    }
    var pad = [0xec, 0x11];
    while (out.length < totalBytes) out.push(pad[out.length % 2]);
    return out;
  }

  function buildBlocks(dataBytes, info) {
    var data = [];
    var ec = [];
    var idx = 0;
    for (var g = 0; g < info.groups.length; g++) {
      var count = info.groups[g][0];
      var perBlock = info.groups[g][1];
      for (var i = 0; i < count; i++) {
        var slice = dataBytes.slice(idx, idx + perBlock);
        idx += perBlock;
        data.push(slice);
        ec.push(rsEncode(slice, info.ec));
      }
    }
    return { data: data, ec: ec };
  }

  function interleave(blocks) {
    var maxData = 0;
    for (var i = 0; i < blocks.data.length; i++) {
      if (blocks.data[i].length > maxData) maxData = blocks.data[i].length;
    }
    var out = [];
    for (var i = 0; i < maxData; i++) {
      for (var j = 0; j < blocks.data.length; j++) {
        if (i < blocks.data[j].length) out.push(blocks.data[j][i]);
      }
    }
    var ecLen = blocks.ec[0].length;
    for (var i = 0; i < ecLen; i++) {
      for (var j = 0; j < blocks.ec.length; j++) out.push(blocks.ec[j][i]);
    }
    return out;
  }

  function makeGrid(size) {
    var m = new Array(size);
    var f = new Array(size);
    for (var i = 0; i < size; i++) {
      m[i] = new Array(size);
      f[i] = new Array(size);
      for (var j = 0; j < size; j++) { m[i][j] = 0; f[i][j] = false; }
    }
    return { m: m, f: f, size: size };
  }

  function placeFinder(g, r, c) {
    for (var dr = -1; dr <= 7; dr++) {
      for (var dc = -1; dc <= 7; dc++) {
        var rr = r + dr, cc = c + dc;
        if (rr < 0 || cc < 0 || rr >= g.size || cc >= g.size) continue;
        var v = 0;
        if (dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6) {
          if (dr === 0 || dr === 6 || dc === 0 || dc === 6) v = 1;
          else if (dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4) v = 1;
        }
        g.m[rr][cc] = v;
        g.f[rr][cc] = true;
      }
    }
  }

  function placeAlignment(g, r, c) {
    for (var dr = -2; dr <= 2; dr++) {
      for (var dc = -2; dc <= 2; dc++) {
        var rr = r + dr, cc = c + dc;
        if (rr < 0 || cc < 0 || rr >= g.size || cc >= g.size) continue;
        var v = 0;
        if (dr === -2 || dr === 2 || dc === -2 || dc === 2) v = 1;
        else if (dr === 0 && dc === 0) v = 1;
        g.m[rr][cc] = v;
        g.f[rr][cc] = true;
      }
    }
  }

  function placeFunctionPatterns(g, version) {
    var size = g.size;
    placeFinder(g, 0, 0);
    placeFinder(g, 0, size - 7);
    placeFinder(g, size - 7, 0);
    // Timing
    for (var i = 8; i < size - 8; i++) {
      g.m[6][i] = (i % 2 === 0) ? 1 : 0;
      g.m[i][6] = (i % 2 === 0) ? 1 : 0;
      g.f[6][i] = true;
      g.f[i][6] = true;
    }
    // Alignment
    var pos = ALIGN[version - 1];
    for (var i = 0; i < pos.length; i++) {
      for (var j = 0; j < pos.length; j++) {
        var r = pos[i], c = pos[j];
        if (r === 6 && c === 6) continue;
        if (r === 6 && c === size - 7) continue;
        if (r === size - 7 && c === 6) continue;
        placeAlignment(g, r, c);
      }
    }
    // Reserve format info (filled later)
    for (var i = 0; i < 9; i++) {
      g.f[8][i] = true;
      g.f[i][8] = true;
    }
    for (var i = 0; i < 8; i++) {
      g.f[size - 1 - i][8] = true;
      g.f[8][size - 1 - i] = true;
    }
    // Dark module
    g.m[size - 8][8] = 1;
    g.f[size - 8][8] = true;
    // Reserve version info (v ≥ 7)
    if (version >= 7) {
      for (var i = 0; i < 6; i++) {
        for (var j = size - 11; j <= size - 9; j++) {
          g.f[i][j] = true;
          g.f[j][i] = true;
        }
      }
    }
  }

  // Mask 0: (i + j) % 2 === 0
  function maskBit(r, c) { return ((r + c) % 2) === 0 ? 1 : 0; }

  function placeData(g, bits) {
    var size = g.size;
    var bitIdx = 0;
    var dir = -1;
    var col = size - 1;
    while (col > 0) {
      if (col === 6) col--;
      var row = (dir === -1) ? size - 1 : 0;
      for (var k = 0; k < size; k++) {
        for (var x = 0; x < 2; x++) {
          var c = col - x;
          if (!g.f[row][c]) {
            var bit = (bitIdx < bits.length) ? bits[bitIdx] : 0;
            g.m[row][c] = bit ^ maskBit(row, c);
            bitIdx++;
          }
        }
        row += dir;
      }
      col -= 2;
      dir = -dir;
    }
  }

  // Format info: ECC level L = 01, mask = 000 → 5 bits = 0b01000 → 0x08.
  // Append BCH(15,5) parity, mask with 0x5412.
  function formatInfoBits() {
    var data = 0x08; // L + mask 0
    var rem = data << 10;
    var poly = 0x537;
    for (var i = 14; i >= 10; i--) {
      if ((rem >> i) & 1) rem ^= poly << (i - 10);
    }
    var bits = ((data << 10) | rem) ^ 0x5412;
    return bits & 0x7fff;
  }

  function placeFormatInfo(g) {
    var bits = formatInfoBits();
    var size = g.size;
    function get(i) { return (bits >> i) & 1; }
    // Top-left horizontal
    for (var i = 0; i < 6; i++) g.m[8][i] = get(i);
    g.m[8][7] = get(6);
    g.m[8][8] = get(7);
    g.m[7][8] = get(8);
    for (var i = 9; i < 15; i++) g.m[14 - i][8] = get(i);
    // Bottom + right
    for (var i = 0; i < 7; i++) g.m[size - 1 - i][8] = get(i);
    for (var i = 7; i < 15; i++) g.m[8][size - 15 + i] = get(i);
  }

  // Version info BCH(18,6) for v ≥ 7.
  function versionInfoBits(version) {
    var rem = version << 12;
    var poly = 0x1f25;
    for (var i = 17; i >= 12; i--) {
      if ((rem >> i) & 1) rem ^= poly << (i - 12);
    }
    return (version << 12) | rem;
  }

  function placeVersionInfo(g, version) {
    if (version < 7) return;
    var bits = versionInfoBits(version);
    var size = g.size;
    for (var i = 0; i < 18; i++) {
      var bit = (bits >> i) & 1;
      var a = Math.floor(i / 3);
      var b = (i % 3) + size - 11;
      g.m[a][b] = bit;
      g.m[b][a] = bit;
    }
  }

  function renderSvg(matrix, scale) {
    var size = matrix.length;
    var quiet = 4;
    var dim = (size + quiet * 2) * scale;
    var parts = [];
    parts.push('<svg xmlns="http://www.w3.org/2000/svg" width="' + dim + '" height="' + dim + '" viewBox="0 0 ' + (size + quiet * 2) + ' ' + (size + quiet * 2) + '" shape-rendering="crispEdges">');
    parts.push('<rect width="100%" height="100%" fill="#fff"/>');
    var path = '';
    for (var r = 0; r < size; r++) {
      for (var c = 0; c < size; c++) {
        if (matrix[r][c]) {
          path += 'M' + (c + quiet) + ',' + (r + quiet) + 'h1v1h-1z';
        }
      }
    }
    parts.push('<path d="' + path + '" fill="#000"/>');
    parts.push('</svg>');
    return parts.join('');
  }

  global.qrEncodeSvg = function (text, scale) {
    scale = scale || 4;
    var bytes = utf8Bytes(text || '');
    var version = pickVersion(bytes.length);
    if (version < 0) throw new Error('text too long for QR (max ~230 chars)');
    var info = VER[version - 1];
    var dataBytes = buildBitstream(bytes, version, info);
    var blocks = buildBlocks(dataBytes, info);
    var bytestream = interleave(blocks);
    var bits = [];
    for (var i = 0; i < bytestream.length; i++) {
      for (var j = 7; j >= 0; j--) bits.push((bytestream[i] >> j) & 1);
    }
    var g = makeGrid(info.size);
    placeFunctionPatterns(g, version);
    placeData(g, bits);
    placeFormatInfo(g);
    placeVersionInfo(g, version);
    return renderSvg(g.m, scale);
  };
})(window);
