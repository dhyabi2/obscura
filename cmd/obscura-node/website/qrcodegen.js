/*
 * QR Code generator (compact port).
 * Copyright (c) Project Nayuki. (MIT License) — https://www.nayuki.io/page/qr-code-generator-library
 * Vendored for the Obscura wallet (offline, no-eval, CSP-safe). Byte-mode only,
 * which is all the wallet needs (Nano addresses / URIs are ASCII).
 */
var qrcodegen = (function () {
  "use strict";

  // --- Reed-Solomon over GF(2^8) with primitive modulus 0x11D ---
  function rsMultiply(x, y) {
    var z = 0;
    for (var i = 7; i >= 0; i--) {
      z = (z << 1) ^ ((z >>> 7) * 0x11D);
      z ^= ((y >>> i) & 1) * x;
    }
    return z & 0xFF;
  }
  function rsDivisor(degree) {
    var result = [];
    for (var i = 0; i < degree - 1; i++) result.push(0);
    result.push(1);
    var root = 1;
    for (var i = 0; i < degree; i++) {
      for (var j = 0; j < result.length; j++) {
        result[j] = rsMultiply(result[j], root);
        if (j + 1 < result.length) result[j] ^= result[j + 1];
      }
      root = rsMultiply(root, 0x02);
    }
    return result;
  }
  function rsRemainder(data, divisor) {
    var result = divisor.map(function () { return 0; });
    data.forEach(function (b) {
      var factor = b ^ result.shift();
      result.push(0);
      divisor.forEach(function (coef, i) { result[i] ^= rsMultiply(coef, factor); });
    });
    return result;
  }

  // ECC tables: [level][version] -> count. Index level 0..3 = L,M,Q,H.
  var ECC_CODEWORDS_PER_BLOCK = [
    [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
    [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
    [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
    [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30]
  ];
  var ECC_BLOCKS = [
    [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
    [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
    [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
    [-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81]
  ];

  function getNumRawDataModules(ver) {
    var result = (16 * ver + 128) * ver + 64;
    if (ver >= 2) {
      var numAlign = Math.floor(ver / 7) + 2;
      result -= (25 * numAlign - 10) * numAlign - 55;
      if (ver >= 7) result -= 36;
    }
    return result;
  }
  function getNumDataCodewords(ver, ecl) {
    return Math.floor(getNumRawDataModules(ver) / 8)
      - ECC_CODEWORDS_PER_BLOCK[ecl][ver] * ECC_BLOCKS[ecl][ver];
  }

  function QrCode(version, ecl, dataCodewords, mask) {
    this.version = version;
    this.size = version * 4 + 17;
    this.ecl = ecl;
    var size = this.size;
    this.modules = [];
    this.isFunction = [];
    for (var i = 0; i < size; i++) {
      this.modules.push(new Array(size).fill(false));
      this.isFunction.push(new Array(size).fill(false));
    }
    this.drawFunctionPatterns();
    var allCodewords = this.addEccAndInterleave(dataCodewords);
    this.drawCodewords(allCodewords);
    if (mask < 0) {
      var minPenalty = Infinity;
      for (var m = 0; m < 8; m++) {
        this.applyMask(m); this.drawFormatBits(m);
        var p = this.getPenaltyScore();
        if (p < minPenalty) { mask = m; minPenalty = p; }
        this.applyMask(m);
      }
    }
    this.mask = mask;
    this.applyMask(mask);
    this.drawFormatBits(mask);
    this.isFunction = null;
  }

  QrCode.prototype.getModule = function (x, y) {
    return 0 <= x && x < this.size && 0 <= y && y < this.size && this.modules[y][x];
  };
  QrCode.prototype.setFunctionModule = function (x, y, isDark) {
    this.modules[y][x] = isDark;
    this.isFunction[y][x] = true;
  };
  QrCode.prototype.drawFunctionPatterns = function () {
    var size = this.size, self = this;
    for (var i = 0; i < size; i++) {
      this.setFunctionModule(6, i, i % 2 === 0);
      this.setFunctionModule(i, 6, i % 2 === 0);
    }
    this.drawFinderPattern(3, 3);
    this.drawFinderPattern(size - 4, 3);
    this.drawFinderPattern(3, size - 4);
    var alignPos = this.getAlignmentPatternPositions();
    var n = alignPos.length;
    for (var i = 0; i < n; i++)
      for (var j = 0; j < n; j++)
        if (!((i === 0 && j === 0) || (i === 0 && j === n - 1) || (i === n - 1 && j === 0)))
          this.drawAlignmentPattern(alignPos[i], alignPos[j]);
    this.drawFormatBits(0);
    this.drawVersion();
  };
  QrCode.prototype.drawFormatBits = function (mask) {
    var data = (this.ecl << 3) | mask;
    var rem = data;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    var bits = ((data << 10) | rem) ^ 0x5412;
    for (var i = 0; i <= 5; i++) this.setFunctionModule(8, i, ((bits >>> i) & 1) !== 0);
    this.setFunctionModule(8, 7, ((bits >>> 6) & 1) !== 0);
    this.setFunctionModule(8, 8, ((bits >>> 7) & 1) !== 0);
    this.setFunctionModule(7, 8, ((bits >>> 8) & 1) !== 0);
    for (var i = 9; i < 15; i++) this.setFunctionModule(14 - i, 8, ((bits >>> i) & 1) !== 0);
    var size = this.size;
    for (var i = 0; i < 8; i++) this.setFunctionModule(size - 1 - i, 8, ((bits >>> i) & 1) !== 0);
    for (var i = 8; i < 15; i++) this.setFunctionModule(8, size - 15 + i, ((bits >>> i) & 1) !== 0);
    this.setFunctionModule(8, size - 8, true);
  };
  QrCode.prototype.drawVersion = function () {
    if (this.version < 7) return;
    var rem = this.version;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1F25);
    var bits = (this.version << 12) | rem;
    for (var i = 0; i < 18; i++) {
      var bit = ((bits >>> i) & 1) !== 0;
      var a = this.size - 11 + (i % 3), b = Math.floor(i / 3);
      this.setFunctionModule(a, b, bit);
      this.setFunctionModule(b, a, bit);
    }
  };
  QrCode.prototype.drawFinderPattern = function (x, y) {
    for (var dy = -4; dy <= 4; dy++)
      for (var dx = -4; dx <= 4; dx++) {
        var dist = Math.max(Math.abs(dx), Math.abs(dy));
        var xx = x + dx, yy = y + dy;
        if (0 <= xx && xx < this.size && 0 <= yy && yy < this.size)
          this.setFunctionModule(xx, yy, dist !== 2 && dist !== 4);
      }
  };
  QrCode.prototype.drawAlignmentPattern = function (x, y) {
    for (var dy = -2; dy <= 2; dy++)
      for (var dx = -2; dx <= 2; dx++)
        this.setFunctionModule(x + dx, y + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  };
  QrCode.prototype.getAlignmentPatternPositions = function () {
    if (this.version === 1) return [];
    var numAlign = Math.floor(this.version / 7) + 2;
    var step = Math.floor((this.version * 8 + numAlign * 3 + 5) / (numAlign * 4 - 4)) * 2;
    var result = [6];
    for (var pos = this.size - 7; result.length < numAlign; pos -= step) result.splice(1, 0, pos);
    return result;
  };
  QrCode.prototype.addEccAndInterleave = function (data) {
    var ver = this.version, ecl = this.ecl;
    var numBlocks = ECC_BLOCKS[ecl][ver];
    var blockEccLen = ECC_CODEWORDS_PER_BLOCK[ecl][ver];
    var rawCodewords = Math.floor(getNumRawDataModules(ver) / 8);
    var numShortBlocks = numBlocks - rawCodewords % numBlocks;
    var shortBlockLen = Math.floor(rawCodewords / numBlocks);
    var blocks = [];
    var rsDiv = rsDivisor(blockEccLen);
    for (var i = 0, k = 0; i < numBlocks; i++) {
      var dat = data.slice(k, k + shortBlockLen - blockEccLen + (i < numShortBlocks ? 0 : 1));
      k += dat.length;
      var ecc = rsRemainder(dat, rsDiv);
      if (i < numShortBlocks) dat.push(0);
      blocks.push(dat.concat(ecc));
    }
    var result = [];
    for (var i = 0; i < blocks[0].length; i++) {
      for (var j = 0; j < blocks.length; j++) {
        if (i !== shortBlockLen - blockEccLen || j >= numShortBlocks)
          result.push(blocks[j][i]);
      }
    }
    return result;
  };
  QrCode.prototype.drawCodewords = function (data) {
    var size = this.size, i = 0;
    for (var right = size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (var vert = 0; vert < size; vert++) {
        for (var j = 0; j < 2; j++) {
          var x = right - j;
          var upward = ((right + 1) & 2) === 0;
          var y = upward ? size - 1 - vert : vert;
          if (!this.isFunction[y][x] && i < data.length * 8) {
            this.modules[y][x] = ((data[i >>> 3] >>> (7 - (i & 7))) & 1) !== 0;
            i++;
          }
        }
      }
    }
  };
  QrCode.prototype.applyMask = function (mask) {
    var size = this.size;
    for (var y = 0; y < size; y++) {
      for (var x = 0; x < size; x++) {
        var invert;
        switch (mask) {
          case 0: invert = (x + y) % 2 === 0; break;
          case 1: invert = y % 2 === 0; break;
          case 2: invert = x % 3 === 0; break;
          case 3: invert = (x + y) % 3 === 0; break;
          case 4: invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break;
          case 5: invert = (x * y) % 2 + (x * y) % 3 === 0; break;
          case 6: invert = ((x * y) % 2 + (x * y) % 3) % 2 === 0; break;
          case 7: invert = ((x + y) % 2 + (x * y) % 3) % 2 === 0; break;
        }
        if (!this.isFunction[y][x] && invert) this.modules[y][x] = !this.modules[y][x];
      }
    }
  };
  QrCode.prototype.getPenaltyScore = function () {
    var size = this.size, result = 0, modules = this.modules;
    for (var y = 0; y < size; y++) {
      var runColor = false, runX = 0, runHistory = [0, 0, 0, 0, 0, 0, 0];
      for (var x = 0; x < size; x++) {
        if (modules[y][x] === runColor) {
          runX++;
          if (runX === 5) result += 3;
          else if (runX > 5) result++;
        } else {
          this._finderPenalty(runX, runHistory); runColor = modules[y][x]; runX = 1;
        }
      }
      result += this._finderTerm(runColor, runX, runHistory) * 40;
    }
    for (var x = 0; x < size; x++) {
      var runColor = false, runY = 0, runHistory = [0, 0, 0, 0, 0, 0, 0];
      for (var y = 0; y < size; y++) {
        if (modules[y][x] === runColor) {
          runY++;
          if (runY === 5) result += 3;
          else if (runY > 5) result++;
        } else {
          this._finderPenalty(runY, runHistory); runColor = modules[y][x]; runY = 1;
        }
      }
      result += this._finderTerm(runColor, runY, runHistory) * 40;
    }
    for (var y = 0; y < size - 1; y++)
      for (var x = 0; x < size - 1; x++) {
        var c = modules[y][x];
        if (c === modules[y][x + 1] && c === modules[y + 1][x] && c === modules[y + 1][x + 1]) result += 3;
      }
    var dark = 0;
    for (var y = 0; y < size; y++) for (var x = 0; x < size; x++) if (modules[y][x]) dark++;
    var total = size * size;
    var k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1;
    result += k * 10;
    return result;
  };
  QrCode.prototype._finderPenalty = function (run, hist) {
    hist.pop(); hist.unshift(run);
  };
  QrCode.prototype._finderTerm = function (runColor, run, hist) {
    // Simplified finder-like detection (good enough for mask selection quality).
    hist.pop(); hist.unshift(run);
    var n = hist[1];
    var core = n > 0 && hist[2] === n && hist[3] === n * 3 && hist[4] === n && hist[5] === n;
    var a = core && hist[0] >= n * 4 && hist[6] >= n ? 1 : 0;
    var b = core && hist[6] >= n * 4 && hist[0] >= n ? 1 : 0;
    return a + b;
  };

  function encodeText(text, ecl) {
    ecl = ecl == null ? 0 : ecl; // default ECC level L
    // byte-mode segment (UTF-8)
    var utf8 = unescape(encodeURIComponent(text));
    var bytes = [];
    for (var i = 0; i < utf8.length; i++) bytes.push(utf8.charCodeAt(i) & 0xFF);

    // choose smallest version (1..40) that fits at this ECC level
    var version = 0, dataCapacityBits = 0, dataUsedBits = 0;
    for (var v = 1; v <= 40; v++) {
      var cap = getNumDataCodewords(v, ecl) * 8;
      var ccBits = v <= 9 ? 8 : 16; // byte-mode char-count bits
      var used = 4 + ccBits + bytes.length * 8;
      if (used <= cap) { version = v; dataCapacityBits = cap; dataUsedBits = used; break; }
    }
    if (version === 0) throw new Error("Data too long for QR");

    // bit buffer
    var bb = [];
    function appendBits(val, len) { for (var i = len - 1; i >= 0; i--) bb.push((val >>> i) & 1); }
    appendBits(0x4, 4); // byte mode
    appendBits(bytes.length, version <= 9 ? 8 : 16);
    for (var i = 0; i < bytes.length; i++) appendBits(bytes[i], 8);
    // terminator + bit/byte padding
    appendBits(0, Math.min(4, dataCapacityBits - bb.length));
    appendBits(0, (8 - bb.length % 8) % 8);
    for (var pad = 0xEC; bb.length < dataCapacityBits; pad ^= 0xEC ^ 0x11) appendBits(pad, 8);
    // pack into bytes
    var dataCodewords = [];
    for (var i = 0; i < bb.length; i += 8) {
      var b = 0;
      for (var j = 0; j < 8; j++) b = (b << 1) | bb[i + j];
      dataCodewords.push(b);
    }
    return new QrCode(version, ecl, dataCodewords, -1);
  }

  return { encodeText: encodeText, Ecc: { LOW: 0, MEDIUM: 1, QUARTILE: 2, HIGH: 3 } };
})();
