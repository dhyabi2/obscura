/*
 * qr.js — self-contained QR Code generator, no dependencies, works offline.
 *
 * This is a compact port of Project Nayuki's "QR Code generator" reference
 * implementation (MIT License, Copyright (c) Project Nayuki), adapted to a
 * single IIFE that renders to a <canvas>. It is a complete, spec-correct
 * encoder (versions 1..40, ECC L/M/Q/H, byte mode), used here only to encode
 * the wallet's receiving address so it can be displayed offline.
 *
 * Original: https://www.nayuki.io/page/qr-code-generator-library
 *
 * Exposes window.OBXQR.render(canvas, text, options) and .generate(text, ecc).
 */
(function () {
  "use strict";

  /* ---- helpers ---- */
  function assert(cond) { if (!cond) throw new Error("Assertion error"); }

  function getBit(x, i) { return ((x >>> i) & 1) !== 0; }

  /* ---- ECC level ---- */
  function Ecc(ordinal, formatBits) { this.ordinal = ordinal; this.formatBits = formatBits; }
  Ecc.LOW = new Ecc(0, 1);
  Ecc.MEDIUM = new Ecc(1, 0);
  Ecc.QUARTILE = new Ecc(2, 3);
  Ecc.HIGH = new Ecc(3, 2);

  /* ---- segment ---- */
  function QrSegment(mode, numChars, bitData) {
    this.mode = mode;
    this.numChars = numChars;
    this.bitData = bitData;
  }
  function Mode(modeBits, ccbits) { this.modeBits = modeBits; this.ccbits = ccbits; }
  Mode.prototype.numCharCountBits = function (ver) {
    return this.ccbits[Math.floor((ver + 7) / 17)];
  };
  Mode.BYTE = new Mode(0x4, [8, 16, 16]);

  function appendBits(val, len, bb) {
    if (len < 0 || len > 31 || (val >>> len) !== 0) throw new RangeError("Value out of range");
    for (var i = len - 1; i >= 0; i--) bb.push((val >>> i) & 1);
  }

  function makeBytes(data) {
    var bb = [];
    for (var i = 0; i < data.length; i++) appendBits(data[i], 8, bb);
    return new QrSegment(Mode.BYTE, data.length, bb);
  }

  function toUtf8Bytes(str) {
    var s = encodeURI(str);
    var result = [];
    for (var i = 0; i < s.length; i++) {
      if (s.charAt(i) !== "%") result.push(s.charCodeAt(i));
      else { result.push(parseInt(s.substring(i + 1, i + 3), 16)); i += 2; }
    }
    return result;
  }

  function getTotalBits(segs, version) {
    var result = 0;
    for (var i = 0; i < segs.length; i++) {
      var seg = segs[i];
      var ccbits = seg.mode.numCharCountBits(version);
      if (seg.numChars >= (1 << ccbits)) return Infinity;
      result += 4 + ccbits + seg.bitData.length;
    }
    return result;
  }

  /* ---- Reed-Solomon ---- */
  function reedSolomonComputeDivisor(degree) {
    if (degree < 1 || degree > 255) throw new RangeError("Degree out of range");
    var result = [];
    for (var i = 0; i < degree - 1; i++) result.push(0);
    result.push(1);
    var root = 1;
    for (var i2 = 0; i2 < degree; i2++) {
      for (var j = 0; j < result.length; j++) {
        result[j] = reedSolomonMultiply(result[j], root);
        if (j + 1 < result.length) result[j] ^= result[j + 1];
      }
      root = reedSolomonMultiply(root, 0x02);
    }
    return result;
  }
  function reedSolomonComputeRemainder(data, divisor) {
    var result = divisor.map(function () { return 0; });
    for (var k = 0; k < data.length; k++) {
      var b = data[k];
      var factor = b ^ result.shift();
      result.push(0);
      for (var i = 0; i < result.length; i++) {
        result[i] ^= reedSolomonMultiply(divisor[i], factor);
      }
    }
    return result;
  }
  function reedSolomonMultiply(x, y) {
    if (x >>> 8 !== 0 || y >>> 8 !== 0) throw new RangeError("Byte out of range");
    var z = 0;
    for (var i = 7; i >= 0; i--) {
      z = (z << 1) ^ ((z >>> 7) * 0x11d);
      z ^= ((y >>> i) & 1) * x;
    }
    return z;
  }

  /* ---- QrCode ---- */
  var MIN_VERSION = 1, MAX_VERSION = 40;
  var ECC_CODEWORDS_PER_BLOCK = [
    [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
    [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
    [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
    [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30]
  ];
  var NUM_ERROR_CORRECTION_BLOCKS = [
    [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
    [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
    [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
    [-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81]
  ];

  function QrCode(version, errorCorrectionLevel, dataCodewords, msk) {
    this.version = version;
    this.errorCorrectionLevel = errorCorrectionLevel;
    this.size = version * 4 + 17;
    var size = this.size;
    var modules = [], isFunction = [];
    var row;
    for (var i = 0; i < size; i++) {
      row = [];
      for (var j = 0; j < size; j++) row.push(false);
      modules.push(row.slice());
      isFunction.push(row.slice());
    }
    this.modules = modules;
    this.isFunction = isFunction;

    this.drawFunctionPatterns();
    var allCodewords = this.addEccAndInterleave(dataCodewords);
    this.drawCodewords(allCodewords);

    if (msk === -1) {
      var minPenalty = Infinity;
      for (var m = 0; m < 8; m++) {
        this.applyMask(m);
        this.drawFormatBits(m);
        var penalty = this.getPenaltyScore();
        if (penalty < minPenalty) { msk = m; minPenalty = penalty; }
        this.applyMask(m);
      }
    }
    assert(0 <= msk && msk <= 7);
    this.mask = msk;
    this.applyMask(msk);
    this.drawFormatBits(msk);
    this.isFunction = [];
  }

  QrCode.encodeText = function (text, ecl) {
    var segs = [makeBytes(toUtf8Bytes(text))];
    return QrCode.encodeSegments(segs, ecl);
  };

  QrCode.encodeSegments = function (segs, ecl, minVersion, maxVersion, mask, boostEcl) {
    if (minVersion === undefined) minVersion = MIN_VERSION;
    if (maxVersion === undefined) maxVersion = MAX_VERSION;
    if (mask === undefined) mask = -1;
    if (boostEcl === undefined) boostEcl = true;

    var version, dataUsedBits;
    for (version = minVersion; ; version++) {
      var dataCapacityBits = QrCode.getNumDataCodewords(version, ecl) * 8;
      var usedBits = getTotalBits(segs, version);
      if (usedBits <= dataCapacityBits) { dataUsedBits = usedBits; break; }
      if (version >= maxVersion) throw new RangeError("Data too long");
    }
    [Ecc.MEDIUM, Ecc.QUARTILE, Ecc.HIGH].forEach(function (newEcl) {
      if (boostEcl && dataUsedBits <= QrCode.getNumDataCodewords(version, newEcl) * 8) ecl = newEcl;
    });

    var bb = [];
    segs.forEach(function (seg) {
      appendBits(seg.mode.modeBits, 4, bb);
      appendBits(seg.numChars, seg.mode.numCharCountBits(version), bb);
      seg.bitData.forEach(function (bit) { bb.push(bit); });
    });
    var dataCapacityBits = QrCode.getNumDataCodewords(version, ecl) * 8;
    appendBits(0, Math.min(4, dataCapacityBits - bb.length), bb);
    appendBits(0, (8 - (bb.length % 8)) % 8, bb);
    for (var padByte = 0xec; bb.length < dataCapacityBits; padByte ^= 0xec ^ 0x11) {
      appendBits(padByte, 8, bb);
    }
    var dataCodewords = [];
    while (dataCodewords.length * 8 < bb.length) dataCodewords.push(0);
    bb.forEach(function (b, i) { dataCodewords[i >>> 3] |= b << (7 - (i & 7)); });

    return new QrCode(version, ecl, dataCodewords, mask);
  };

  QrCode.getNumRawDataModules = function (ver) {
    var result = (16 * ver + 128) * ver + 64;
    if (ver >= 2) {
      var numAlign = Math.floor(ver / 7) + 2;
      result -= (25 * numAlign - 10) * numAlign - 55;
      if (ver >= 7) result -= 36;
    }
    return result;
  };

  QrCode.getNumDataCodewords = function (ver, ecl) {
    return Math.floor(QrCode.getNumRawDataModules(ver) / 8) -
      ECC_CODEWORDS_PER_BLOCK[ecl.ordinal][ver] *
      NUM_ERROR_CORRECTION_BLOCKS[ecl.ordinal][ver];
  };

  QrCode.prototype.getModule = function (x, y) {
    return 0 <= x && x < this.size && 0 <= y && y < this.size && this.modules[y][x];
  };

  QrCode.prototype.drawFunctionPatterns = function () {
    var size = this.size;
    for (var i = 0; i < size; i++) {
      this.setFunctionModule(6, i, i % 2 === 0);
      this.setFunctionModule(i, 6, i % 2 === 0);
    }
    this.drawFinderPattern(3, 3);
    this.drawFinderPattern(size - 4, 3);
    this.drawFinderPattern(3, size - 4);

    var alignPatPos = this.getAlignmentPatternPositions();
    var numAlign = alignPatPos.length;
    for (var a = 0; a < numAlign; a++) {
      for (var b = 0; b < numAlign; b++) {
        if (!((a === 0 && b === 0) || (a === 0 && b === numAlign - 1) || (a === numAlign - 1 && b === 0)))
          this.drawAlignmentPattern(alignPatPos[a], alignPatPos[b]);
      }
    }
    this.drawFormatBits(0);
    this.drawVersion();
  };

  QrCode.prototype.drawFormatBits = function (mask) {
    var data = (this.errorCorrectionLevel.formatBits << 3) | mask;
    var rem = data;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    var bits = ((data << 10) | rem) ^ 0x5412;
    assert(bits >>> 15 === 0);
    for (var i2 = 0; i2 <= 5; i2++) this.setFunctionModule(8, i2, getBit(bits, i2));
    this.setFunctionModule(8, 7, getBit(bits, 6));
    this.setFunctionModule(8, 8, getBit(bits, 7));
    this.setFunctionModule(7, 8, getBit(bits, 8));
    for (var i3 = 9; i3 < 15; i3++) this.setFunctionModule(14 - i3, 8, getBit(bits, i3));
    var size = this.size;
    for (var i4 = 0; i4 < 8; i4++) this.setFunctionModule(size - 1 - i4, 8, getBit(bits, i4));
    for (var i5 = 8; i5 < 15; i5++) this.setFunctionModule(8, size - 15 + i5, getBit(bits, i5));
    this.setFunctionModule(8, size - 8, true);
  };

  QrCode.prototype.drawVersion = function () {
    if (this.version < 7) return;
    var rem = this.version;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    var bits = (this.version << 12) | rem;
    assert(bits >>> 18 === 0);
    for (var i2 = 0; i2 < 18; i2++) {
      var color = getBit(bits, i2);
      var a = this.size - 11 + (i2 % 3);
      var b = Math.floor(i2 / 3);
      this.setFunctionModule(a, b, color);
      this.setFunctionModule(b, a, color);
    }
  };

  QrCode.prototype.drawFinderPattern = function (x, y) {
    for (var dy = -4; dy <= 4; dy++) {
      for (var dx = -4; dx <= 4; dx++) {
        var dist = Math.max(Math.abs(dx), Math.abs(dy));
        var xx = x + dx, yy = y + dy;
        if (0 <= xx && xx < this.size && 0 <= yy && yy < this.size)
          this.setFunctionModule(xx, yy, dist !== 2 && dist !== 4);
      }
    }
  };

  QrCode.prototype.drawAlignmentPattern = function (x, y) {
    for (var dy = -2; dy <= 2; dy++)
      for (var dx = -2; dx <= 2; dx++)
        this.setFunctionModule(x + dx, y + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  };

  QrCode.prototype.setFunctionModule = function (x, y, isDark) {
    this.modules[y][x] = isDark;
    this.isFunction[y][x] = true;
  };

  QrCode.prototype.addEccAndInterleave = function (data) {
    var ver = this.version, ecl = this.errorCorrectionLevel;
    if (data.length !== QrCode.getNumDataCodewords(ver, ecl)) throw new RangeError("Invalid argument");
    var numBlocks = NUM_ERROR_CORRECTION_BLOCKS[ecl.ordinal][ver];
    var blockEccLen = ECC_CODEWORDS_PER_BLOCK[ecl.ordinal][ver];
    var rawCodewords = Math.floor(QrCode.getNumRawDataModules(ver) / 8);
    var numShortBlocks = numBlocks - (rawCodewords % numBlocks);
    var shortBlockLen = Math.floor(rawCodewords / numBlocks);

    var blocks = [];
    var rsDiv = reedSolomonComputeDivisor(blockEccLen);
    var k = 0;
    for (var i = 0; i < numBlocks; i++) {
      var dat = data.slice(k, k + shortBlockLen - blockEccLen + (i < numShortBlocks ? 0 : 1));
      k += dat.length;
      var ecc = reedSolomonComputeRemainder(dat, rsDiv);
      if (i < numShortBlocks) dat.push(0);
      blocks.push(dat.concat(ecc));
    }

    var result = [];
    for (var idx = 0; idx < blocks[0].length; idx++) {
      blocks.forEach(function (block, j) {
        if (idx !== shortBlockLen - blockEccLen || j >= numShortBlocks) result.push(block[idx]);
      });
    }
    assert(result.length === rawCodewords);
    return result;
  };

  QrCode.prototype.drawCodewords = function (data) {
    var size = this.size;
    var i = 0;
    for (var right = size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (var vert = 0; vert < size; vert++) {
        for (var j = 0; j < 2; j++) {
          var x = right - j;
          var upward = ((right + 1) & 2) === 0;
          var y = upward ? size - 1 - vert : vert;
          if (!this.isFunction[y][x] && i < data.length * 8) {
            this.modules[y][x] = getBit(data[i >>> 3], 7 - (i & 7));
            i++;
          }
        }
      }
    }
  };

  QrCode.prototype.applyMask = function (mask) {
    for (var y = 0; y < this.size; y++) {
      for (var x = 0; x < this.size; x++) {
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
          default: throw new Error("unreachable");
        }
        if (!this.isFunction[y][x] && invert) this.modules[y][x] = !this.modules[y][x];
      }
    }
  };

  QrCode.prototype.getPenaltyScore = function () {
    var size = this.size, result = 0;
    var PENALTY_N1 = 3, PENALTY_N2 = 3, PENALTY_N3 = 40, PENALTY_N4 = 10;
    for (var y = 0; y < size; y++) {
      var runColor = false, runX = 0, runHistory = [0, 0, 0, 0, 0, 0, 0];
      for (var x = 0; x < size; x++) {
        if (this.modules[y][x] === runColor) {
          runX++;
          if (runX === 5) result += PENALTY_N1;
          else if (runX > 5) result++;
        } else {
          this.finderPenaltyAddHistory(runX, runHistory);
          if (!runColor) result += this.finderPenaltyCountPatterns(runHistory) * PENALTY_N3;
          runColor = this.modules[y][x];
          runX = 1;
        }
      }
      result += this.finderPenaltyTerminateAndCount(runColor, runX, runHistory) * PENALTY_N3;
    }
    for (var x2 = 0; x2 < size; x2++) {
      var runColor2 = false, runY = 0, runHistory2 = [0, 0, 0, 0, 0, 0, 0];
      for (var y2 = 0; y2 < size; y2++) {
        if (this.modules[y2][x2] === runColor2) {
          runY++;
          if (runY === 5) result += PENALTY_N1;
          else if (runY > 5) result++;
        } else {
          this.finderPenaltyAddHistory(runY, runHistory2);
          if (!runColor2) result += this.finderPenaltyCountPatterns(runHistory2) * PENALTY_N3;
          runColor2 = this.modules[y2][x2];
          runY = 1;
        }
      }
      result += this.finderPenaltyTerminateAndCount(runColor2, runY, runHistory2) * PENALTY_N3;
    }
    for (var y3 = 0; y3 < size - 1; y3++) {
      for (var x3 = 0; x3 < size - 1; x3++) {
        var color = this.modules[y3][x3];
        if (color === this.modules[y3][x3 + 1] && color === this.modules[y3 + 1][x3] && color === this.modules[y3 + 1][x3 + 1])
          result += PENALTY_N2;
      }
    }
    var dark = 0;
    this.modules.forEach(function (row) { row.forEach(function (c) { if (c) dark++; }); });
    var total = size * size;
    var k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1;
    result += k * PENALTY_N4;
    return result;
  };

  QrCode.prototype.getAlignmentPatternPositions = function () {
    if (this.version === 1) return [];
    var numAlign = Math.floor(this.version / 7) + 2;
    var step = (this.version === 32) ? 26 :
      Math.ceil((this.version * 4 + 4) / (numAlign * 2 - 2)) * 2;
    var result = [6];
    for (var pos = this.size - 7; result.length < numAlign; pos -= step) result.splice(1, 0, pos);
    return result;
  };

  QrCode.prototype.finderPenaltyCountPatterns = function (runHistory) {
    var n = runHistory[1];
    var core = n > 0 && runHistory[2] === n && runHistory[3] === n * 3 && runHistory[4] === n && runHistory[5] === n;
    return (core && runHistory[0] >= n * 4 && runHistory[6] >= n ? 1 : 0) +
      (core && runHistory[6] >= n * 4 && runHistory[0] >= n ? 1 : 0);
  };
  QrCode.prototype.finderPenaltyTerminateAndCount = function (currentRunColor, currentRunLength, runHistory) {
    if (currentRunColor) {
      this.finderPenaltyAddHistory(currentRunLength, runHistory);
      currentRunLength = 0;
    }
    currentRunLength += this.size;
    this.finderPenaltyAddHistory(currentRunLength, runHistory);
    return this.finderPenaltyCountPatterns(runHistory);
  };
  QrCode.prototype.finderPenaltyAddHistory = function (currentRunLength, runHistory) {
    if (runHistory[0] === 0) currentRunLength += this.size;
    runHistory.pop();
    runHistory.unshift(currentRunLength);
  };

  /* ---- public API ---- */
  var ECC_MAP = { L: Ecc.LOW, M: Ecc.MEDIUM, Q: Ecc.QUARTILE, H: Ecc.HIGH };

  function generate(text, eccName) {
    var ecl = ECC_MAP[eccName || "M"] || Ecc.MEDIUM;
    var qr = QrCode.encodeText(text, ecl);
    var size = qr.size;
    var matrix = [];
    for (var y = 0; y < size; y++) {
      var row = [];
      for (var x = 0; x < size; x++) row.push(qr.modules[y][x] ? 1 : 0);
      matrix.push(row);
    }
    return matrix;
  }

  function render(canvas, text, options) {
    options = options || {};
    var matrix = generate(text, options.level || "M");
    var n = matrix.length;
    var quiet = options.quiet != null ? options.quiet : 4;
    var total = n + quiet * 2;
    var px = Math.max(1, Math.floor((options.size || canvas.width || 200) / total));
    var dim = px * total;
    canvas.width = dim;
    canvas.height = dim;
    var ctx = canvas.getContext("2d");
    ctx.fillStyle = options.light || "#ffffff";
    ctx.fillRect(0, 0, dim, dim);
    ctx.fillStyle = options.dark || "#000000";
    for (var y = 0; y < n; y++)
      for (var x = 0; x < n; x++)
        if (matrix[y][x]) ctx.fillRect((x + quiet) * px, (y + quiet) * px, px, px);
    return { modules: n, scale: px };
  }

  window.OBXQR = { render: render, generate: generate };
})();
