"""Regenerate original GO-only font fixtures; fonttools/brotli are development tools.
No third-party font file or glyph outline is used. Runtime builds embed outputs.
"""
from pathlib import Path
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen

patterns = {
    '.notdef': ['11111', '10001', '10001', '10001', '10001', '10001', '11111'],
    'space': [],
    'G': ['01110', '10001', '10000', '10111', '10001', '10001', '01110'],
    'O': ['01110', '10001', '10001', '10001', '10001', '10001', '01110'],
}
glyphs = {}
for name, rows in patterns.items():
    pen = TTGlyphPen(None)
    for row, bits in enumerate(rows):
        for column, bit in enumerate(bits):
            if bit != '1':
                continue
            x, y = 50 + column * 130, 750 - row * 130
            pen.moveTo((x, y))
            pen.lineTo((x, y + 110))
            pen.lineTo((x + 110, y + 110))
            pen.lineTo((x + 110, y))
            pen.closePath()
    glyphs[name] = pen.glyph()

builder = FontBuilder(1000, isTTF=True)
builder.setupGlyphOrder(list(patterns))
builder.setupCharacterMap({32: 'space', 71: 'G', 79: 'O'})
builder.setupGlyf(glyphs)
builder.setupHorizontalMetrics({name: (760, 50) for name in patterns})
builder.setupHorizontalHeader(ascent=900, descent=-200)
builder.setupNameTable({
    'familyName': 'Nextgo Fixture', 'styleName': 'Regular',
    'uniqueFontIdentifier': 'Nextgo Fixture 1.0',
    'fullName': 'Nextgo Fixture Regular', 'psName': 'NextgoFixture-Regular',
    'version': 'Version 1.0',
    'copyright': 'Original Next.go demonstration glyphs. MIT License.',
})
builder.setupOS2(sTypoAscender=900, sTypoDescender=-200, usWinAscent=900, usWinDescent=200)
builder.setupPost()
builder.setupMaxp()
builder.font.recalcTimestamp = False
builder.font['head'].created = builder.font['head'].modified = 2082844800
root = Path(__file__).resolve().parents[2] / 'examples/demo/localfonts'
for flavor in ['woff', 'woff2']:
    builder.font.flavor = flavor
    builder.font.save(root / ('fixture.' + flavor))
    print(flavor, (root / ('fixture.' + flavor)).stat().st_size)
