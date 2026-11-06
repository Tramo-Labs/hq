#!/usr/bin/env python3
"""Writes the synthetic bank exports used by the ledger tests.

Every name, account number and amount here is invented. Run from this
directory: python3 gen_fixtures.py. The outputs are committed; re-running
must produce identical files.
"""

OPEN = 1_000_000_00  # cents

# (day, description, reference, cents signed)
OCT = [
    (1, "TRANSFERENCIA RECIBIDA CLIENTE UNO", "000101", 250_000_00),
    (2, "PAGO PROVEEDOR EJEMPLO SA", "000102", -120_500_50),
    (2, "COMISION MANTENIMIENTO CUENTA", "000103", -8_500_00),
    (3, "IVA 21% S/COMISION", "000104", -1_785_00),
    (6, "DEPOSITO EN EFECTIVO", "000105", 45_000_00),
    (6, "TRANSFERENCIA RECIBIDA CLIENTE DOS", "000106", 100_000_00),
    (6, "TRANSFERENCIA RECIBIDA CLIENTE DOS", "000106", 100_000_00),  # genuine twin
    (8, "IMP LEY 25413 DEBITO", "000107", -723_00),
    (9, "ECHEQ 00012345 COBRADO", "000108", 310_250_75),
    (10, "PERCEPCION IIBB SIRCREB", "000109", -9_307_52),
]


def ar(c):
    """1234567 cents -> '12.345,67' (Argentine format, sign in front)."""
    sign = "-" if c < 0 else ""
    c = abs(c)
    ent, dec = divmod(c, 100)
    s = f"{ent:,}".replace(",", ".")
    return f"{sign}{s},{dec:02d}"


def dot(c):
    sign = "-" if c < 0 else ""
    c = abs(c)
    return f"{sign}{c // 100}.{c % 100:02d}"


def running(rows, start=OPEN):
    out, bal = [], start
    for d, desc, ref, amt in rows:
        bal += amt
        out.append((d, desc, ref, amt, bal))
    return out


def write(name, text, encoding="utf-8"):
    with open(name, "w", encoding=encoding, newline="") as f:
        f.write(text)


def galicia(rows, extra_col=False):
    head = ["Fecha", "Descripción", "Origen", "Débitos", "Créditos", "Grupo de Conceptos", "Concepto",
            "Número de Terminal", "Observaciones Cliente", "Número de Comprobante", "Leyendas Adicionales 1",
            "Leyendas Adicionales 2", "Tipo de Movimiento", "Saldo"]
    if extra_col:
        head.insert(13, "Código de Operación")
    lines = [";".join(head)]
    for d, desc, ref, amt, bal in reversed(running(rows)):  # newest first
        cells = [f"{d:02d}/10/2026", desc, "Sucursal", ar(-amt) if amt < 0 else "", ar(amt) if amt > 0 else "",
                 "Varios", "Varios", "", "", ref, "", "", "Pesos", ar(bal)]
        if extra_col:
            cells.insert(13, "X1")
        lines.append(";".join(cells))
    return "\r\n".join(lines) + "\r\n"


def santander(rows, trailer=True, opening=OPEN):
    r = running(rows, opening)
    lines = ["Cuenta;Cuenta Corriente en Pesos 000-000001/0",
             "Período;01/10/2026;31/10/2026",
             f"Saldo inicial;{ar(opening)}",
             "",
             "Fecha;Sucursal origen;Descripción;Referencia;Importe;Saldo"]
    for d, desc, ref, amt, bal in r:
        lines.append(f"{d:02d}/10/2026;001;{desc};{ref};{ar(amt)};{ar(bal)}")
    if trailer:
        lines += ["", f"Saldo final;{ar(r[-1][4])}"]
    return "\n".join(lines) + "\n"


def bancor(rows, opening=OPEN, month=10, break_line=None):
    lines = ["Fecha,Concepto,Comprobante,Débito,Crédito,Saldo"]
    for i, (d, desc, ref, amt, bal) in enumerate(running(rows, opening)):
        if break_line == i:
            bal += 1_00
        deb = f'"{ar(-amt)}"' if amt < 0 else ""
        cre = f'"{ar(amt)}"' if amt > 0 else ""
        lines.append(f'{d:02d}/{month:02d}/2026,"{desc}",{ref},{deb},{cre},"{ar(bal)}"')
    return "\n".join(lines) + "\n"


def macro(rows, bad_amount=False):
    lines = ["Fecha\tNro. Comprobante\tDescripción\tImporte\tSaldo"]
    for i, (d, desc, ref, amt, bal) in enumerate(running(rows)):
        a = ar(amt)
        if bad_amount and i == 3:
            a = a.replace(",", ".")  # decimal point in a decimal comma file
        lines.append(f"{d:02d}/10/26\t{ref}\t{desc}\t{a}\t{ar(bal)}")
    return "\n".join(lines) + "\n"


MP_HEAD = ("DATE,SOURCE_ID,EXTERNAL_REFERENCE,RECORD_TYPE,DESCRIPTION,NET_CREDIT_AMOUNT,NET_DEBIT_AMOUNT,"
           "GROSS_AMOUNT,MP_FEE_AMOUNT,FINANCING_FEE_AMOUNT,SHIPPING_FEE_AMOUNT,TAXES_AMOUNT,COUPON_AMOUNT,"
           "INSTALLMENTS,PAYMENT_METHOD")

# (day, source id, external ref, kind, gross, fee, taxes) ; withdrawals have gross None
MP_ROWS = [
    (1, "90000000001", "PEDIDO-1001", "payment", 10_000_00, -410_00, -120_00),
    (1, "90000000002", "PEDIDO-1002", "payment", 25_500_00, -1_045_50, -306_00),
    (3, "90000000003", "PEDIDO-1003", "payment", 7_999_99, -328_00, -96_00),
    (5, "90000000004", "", "withdrawal", None, -30_000_00, 0),
]


def mercadopago(fee_mismatch=False, total=True):
    opening = 5_000_00
    lines = [MP_HEAD, f"2026-10-01T00:00:00.000-03:00,,,initial_available_balance,,{dot(opening)},0.00,,,,,,,,"]
    bal = opening
    for i, (d, sid, ext, kind, gross, fee, tax) in enumerate(MP_ROWS):
        ts = f"2026-10-{d:02d}T12:30:00.000-03:00"
        if gross is None:
            net = fee
            bal += net
            lines.append(f"{ts},{sid},{ext},release,{kind},0.00,{dot(-net)},,,,,,,,")
            continue
        net = gross + fee + tax
        bal += net
        shown_fee = fee - 1_00 if (fee_mismatch and i == 1) else fee
        lines.append(f"{ts},{sid},{ext},release,{kind},{dot(net)},0.00,{dot(gross)},{dot(shown_fee)},0.00,0.00,"
                     f"{dot(tax)},0.00,1,account_money")
    if total:
        lines.append(f"2026-10-31T23:59:59.000-03:00,,,total,,{dot(bal)},0.00,,,,,,,,")
    return "\n".join(lines) + "\n"


NOV = [
    (2, "TRANSFERENCIA RECIBIDA CLIENTE UNO", "000201", 80_000_00),
    (4, "PAGO PROVEEDOR EJEMPLO SA", "000202", -50_000_00),
]


def main():
    write("galicia_ok.csv", galicia(OCT), encoding="latin-1")
    write("galicia_changed_layout.csv", galicia(OCT, extra_col=True), encoding="latin-1")
    write("santander_ok.csv", santander(OCT))
    write("santander_truncated.csv", santander(OCT[:6], trailer=False))
    write("bancor_ok.csv", bancor(OCT))
    write("bancor_bad_balance.csv", bancor(OCT, break_line=4))
    # Two overlapping exports: days 1 to 6 and days 6 to 10 of the same account.
    write("bancor_part1.csv", bancor(OCT[:7]))
    part2_open = OPEN + sum(a for *_, a in OCT[:4])
    write("bancor_part2.csv", bancor(OCT[4:], opening=part2_open))
    oct_close = OPEN + sum(a for *_, a in OCT)
    write("bancor_nov.csv", bancor(NOV, opening=oct_close, month=11))
    write("bancor_nov_gap.csv", bancor(NOV, opening=oct_close + 5_000_00, month=11))
    write("macro_ok.txt", macro(OCT))
    write("macro_bad_amount.txt", macro(OCT, bad_amount=True))
    write("mercadopago_ok.csv", mercadopago())
    write("mercadopago_fee_mismatch.csv", mercadopago(fee_mismatch=True))
    write("mercadopago_no_total.csv", mercadopago(total=False))


if __name__ == "__main__":
    main()
