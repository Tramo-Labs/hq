#!/usr/bin/env python3
"""Writes the synthetic Excel (.xlsx) exports used by the ledger tests.

Every name, account number and amount is invented (the movements are the
same as in gen_fixtures.py). Written with zipfile and plain XML, so no
library is needed and the bytes are reproducible: fixed timestamps, fixed
part order. Run from this directory: python3 gen_xlsx.py.

Styles: 0 general, 1 a date (built-in format 14), 2 a number with two
decimals (built-in format 4).
"""

import datetime
import zipfile
from xml.sax.saxutils import escape

from gen_fixtures import NOV, OCT, OPEN, ar, dot, running

BANCOR_HEAD = ["Fecha", "Concepto", "Comprobante", "Débito", "Crédito", "Saldo"]
NS = 'xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"'
RNS = 'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"'
PKG = "http://schemas.openxmlformats.org/package/2006"
DOC = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
XML = '<?xml version="1.0" encoding="UTF-8"?>'


def serial(month, d):
    return (datetime.date(2026, month, d) - datetime.date(1899, 12, 30)).days


def col(i):
    s, i = "", i + 1
    while i:
        i, r = divmod(i - 1, 26)
        s = chr(65 + r) + s
    return s


class Book:
    def __init__(self):
        self.strings, self.index, self.sheets = [], {}, []

    def shared(self, text):
        if text not in self.index:
            self.index[text] = len(self.strings)
            self.strings.append(text)
        return self.index[text]

    def sheet(self, name, rows):
        """rows: (row number, cells); a cell is None, ("s", shared text),
        ("t", inline text), ("d", date serial) or ("n", number text)."""
        out = []
        for rn, cells in rows:
            cs = []
            for i, c in enumerate(cells):
                if c is None:
                    continue
                ref = f"{col(i)}{rn}"
                kind, v = c
                if kind == "s":
                    cs.append(f'<c r="{ref}" t="s"><v>{self.shared(v)}</v></c>')
                elif kind == "t":
                    cs.append(f'<c r="{ref}" t="inlineStr"><is><t>{escape(v)}</t></is></c>')
                elif kind == "d":
                    cs.append(f'<c r="{ref}" s="1"><v>{v}</v></c>')
                elif kind == "n":
                    cs.append(f'<c r="{ref}" s="2"><v>{v}</v></c>')
            out.append(f'<row r="{rn}">{"".join(cs)}</row>')
        self.sheets.append((name, "".join(out)))

    def save(self, name):
        parts = {
            "[Content_Types].xml": f'{XML}<Types xmlns="{PKG}/content-types">'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/></Types>',
            "_rels/.rels": f'{XML}<Relationships xmlns="{PKG}/relationships">'
            f'<Relationship Id="rId1" Type="{DOC}/officeDocument" Target="xl/workbook.xml"/></Relationships>',
            "xl/workbook.xml": f"{XML}<workbook {NS} {RNS}><sheets>"
            + "".join(f'<sheet name="{escape(n)}" sheetId="{i + 1}" r:id="rId{i + 1}"/>'
                      for i, (n, _) in enumerate(self.sheets))
            + "</sheets></workbook>",
            "xl/_rels/workbook.xml.rels": f'{XML}<Relationships xmlns="{PKG}/relationships">'
            + "".join(f'<Relationship Id="rId{i + 1}" Type="{DOC}/worksheet" Target="worksheets/sheet{i + 1}.xml"/>'
                      for i in range(len(self.sheets)))
            + "</Relationships>",
            "xl/styles.xml": f'{XML}<styleSheet {NS}><cellXfs count="3">'
            '<xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="4"/></cellXfs></styleSheet>',
        }
        for i, (_, body) in enumerate(self.sheets):
            parts[f"xl/worksheets/sheet{i + 1}.xml"] = f"{XML}<worksheet {NS}><sheetData>{body}</sheetData></worksheet>"
        parts["xl/sharedStrings.xml"] = (f"{XML}<sst {NS}>"
                                         + "".join(f"<si><t>{escape(t)}</t></si>" for t in self.strings) + "</sst>")
        with zipfile.ZipFile(name, "w") as z:
            for n, body in parts.items():
                info = zipfile.ZipInfo(n, date_time=(2026, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(info, body.encode("utf-8"))


def num(c, noise=False):
    """Cents to the text Excel stores; noise mimics a computed float."""
    t = dot(c)
    if noise and c % 100:
        t += "0000000001"
    return t


def bancor_rows(rows, opening, month=10, first_row=1, text=False, noise=False, bad_decimals_at=None,
                head=BANCOR_HEAD):
    out = [(first_row, [("s", h) for h in head])]
    for i, (d, desc, ref, amt, bal) in enumerate(running(rows, opening)):
        if text:
            date = ("s", f"{d:02d}/{month:02d}/2026")
            deb = ("s", ar(-amt)) if amt < 0 else None
            cre = ("s", ar(amt)) if amt > 0 else None
            sal = ("s", ar(bal))
        else:
            date = ("d", serial(month, d))
            deb = ("n", num(-amt, noise)) if amt < 0 else None
            cre = ("n", num(amt, noise)) if amt > 0 else None
            sal = ("n", num(bal, noise))
        if bad_decimals_at == i:
            sal = ("n", dot(bal) + "5")
        out.append((first_row + 1 + i, [date, ("s", desc), ("t", ref), deb, cre, sal]))
    return out


def cover(book):
    book.sheet("Resumen", [
        (1, [("s", "Bancor - Consulta de movimientos")]),
        (2, [("s", "Cuenta"), ("s", "CC $ 000-000001/0")]),
        (3, [("s", "Fecha de emisión"), ("d", serial(11, 1))]),
        (4, [("s", "Titular"), ("s", "EMPRESA EJEMPLO SA")]),
    ])


def main():
    oct_close = OPEN + sum(a for *_, a in OCT)
    half = OPEN + sum(a for *_, a in OCT[:5])

    # A cover sheet, then the movements under a title, with Excel numbers
    # carrying float noise.
    b = Book()
    cover(b)
    b.sheet("Movimientos", [(1, [("s", "Movimientos de la cuenta")])] + bancor_rows(OCT, OPEN, first_row=3, noise=True))
    b.save("bancor_ok.xlsx")

    # One month in two sheets, the later half first in the workbook; the
    # first half has its dates and amounts typed as text.
    b = Book()
    b.sheet("Hoja 2", bancor_rows(OCT[5:], half))
    b.sheet("Hoja 1", bancor_rows(OCT[:5], OPEN, text=True))
    b.save("bancor_two_sheets.xlsx")

    # The second sheet does not open where the first one closed.
    b = Book()
    b.sheet("Hoja 1", bancor_rows(OCT[:5], OPEN))
    b.sheet("Hoja 2", bancor_rows(OCT[5:], half + 100_00))
    b.save("bancor_sheets_gap.xlsx")

    # A column more: the fingerprint changes.
    b = Book()
    b.sheet("Movimientos", bancor_rows(OCT, OPEN, head=BANCOR_HEAD + ["Sucursal"]))
    b.save("bancor_changed_layout.xlsx")

    # A known sheet plus a sheet of movements under a header nobody knows.
    b = Book()
    b.sheet("Movimientos", bancor_rows(OCT, OPEN))
    b.sheet("Otra", bancor_rows(NOV, oct_close, month=11, head=["Fecha", "Detalle", "Nro", "Debe", "Haber", "Saldo"]))
    b.save("bancor_stray_sheet.xlsx")

    # A balance with three decimals on the fourth movement.
    b = Book()
    b.sheet("Movimientos", bancor_rows(OCT, OPEN, bad_decimals_at=3))
    b.save("bancor_bad_decimals.xlsx")


if __name__ == "__main__":
    main()
