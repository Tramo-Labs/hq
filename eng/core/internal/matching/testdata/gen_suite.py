#!/usr/bin/env python3
"""Writes suite_sintetica.json: 200 bank movements of an invented pyme, the
open items they settle and the expected outcome of each movement.

Everything is synthetic and labeled by construction, not by hand on real
data: it is a regression suite for the engine, not a measure of how it does
on a client. The real measure needs a client's exports labeled by a person.

Run from this directory: python3 gen_suite.py
"""
import json
import random
from datetime import date, timedelta

rnd = random.Random(20261106)
START = date(2026, 10, 1)

moves, items, labels = [], [], {}
_amount_slot = [0]


def amount():
    """Amounts at least $1.000 apart so no two collide by accident."""
    _amount_slot[0] += 1
    return 1_000_00 * (10 + _amount_slot[0] * 3) + rnd.randint(1, 99_99)


def day(n):
    return (START + timedelta(days=n)).isoformat()


def move(mid, d, amt, desc, ref="", ext="", gross=None, fees=None):
    m = {"id": mid, "date": day(d), "amount": amt, "description": desc, "reference": ref, "external_ref": ext}
    if gross is not None:
        m["gross"] = gross
        m["fees"] = fees
    moves.append(m)


def item(iid, d, amt, refs=(), kind="invoice", party=""):
    items.append({"id": iid, "kind": kind, "date": day(d), "amount": amt, "refs": list(refs),
                  "confidence": 1.0, "party": party})


def label(mid, outcome, tray="", rule="", its=()):
    labels[mid] = {"outcome": outcome, "tray": tray, "rule": rule, "items": sorted(its)}


# A. 70 customer transfers that name the invoice in the description.
for k in range(70):
    a, d = amount(), rnd.randint(0, 27)
    inv = f"0003-{1000 + k:08d}"
    item(f"inv-a{k}", max(0, d - rnd.randint(5, 25)), a, [inv], party=f"CLIENTE {k}")
    move(f"mv-a{k}", d, a, f"TRANSF RECIBIDA CLIENTE {k} FC {inv}", ref=f"{500000 + k}")
    label(f"mv-a{k}", "match", "auto", "reference_exact", [f"inv-a{k}"])

# B. 40 supplier payments without reference, within the date window.
for k in range(40):
    a, d = amount(), rnd.randint(2, 27)
    item(f"pay-b{k}", d + rnd.randint(-5, 5), -a, kind="payment", party=f"PROVEEDOR {k}")
    move(f"mv-b{k}", d, -a, f"TRANSFERENCIA A PROVEEDOR {k}", ref=f"{600000 + k}")
    label(f"mv-b{k}", "match", "auto", "amount_date_window", [f"pay-b{k}"])

# C. 15 cheques by number, cashed well after the date window.
for k in range(15):
    a, d = amount(), rnd.randint(15, 29)
    num = f"{rnd.randint(10000000, 99999999)}"
    sign = 1 if k % 2 == 0 else -1
    item(f"chq-c{k}", d - rnd.randint(10, 14), sign * a, [f"CH {num}"], kind="payment")
    desc = f"ECHEQ {num} COBRADO" if sign > 0 else f"CHEQUE {num} DEBITADO"
    move(f"mv-c{k}", d, sign * a, desc)
    label(f"mv-c{k}", "match", "auto", "reference_exact", [f"chq-c{k}"])

# D. 20 Mercado Pago settlements: net in the account, fees documented.
for k in range(20):
    gross, d = amount(), rnd.randint(0, 27)
    fee = -round(gross * 0.041)
    tax = -round(gross * 0.012)
    net = gross + fee + tax
    order = f"PEDIDO-{2000 + k}"
    item(f"ord-d{k}", d, gross, [order], kind="invoice")
    move(f"mv-d{k}", d, net, "payment", ref=f"{91000000000 + k}", ext=order, gross=gross,
         fees=[{"kind": "mp_fee", "amount": fee}, {"kind": "taxes", "amount": tax}])
    label(f"mv-d{k}", "match", "suggested", "net_of_documented_fees", [f"ord-d{k}"])

# E. 8 transfers that differ from the invoice by a few pesos.
for k in range(8):
    a, d = amount(), rnd.randint(1, 27)
    diff = rnd.choice([-1, 1]) * rnd.randint(1, 999)
    item(f"inv-e{k}", d - rnd.randint(0, 4), a)
    move(f"mv-e{k}", d, a + diff, f"TRANSF RECIBIDA CLIENTE E{k}")
    label(f"mv-e{k}", "match", "suggested", "amount_tolerance", [f"inv-e{k}"])

# F. 10 deposits that group two to four invoices of the same customer.
for k in range(10):
    d = rnd.randint(3, 27)
    n = rnd.randint(2, 4)
    ids, total = [], 0
    for j in range(n):
        a = amount()
        total += a
        ids.append(f"inv-f{k}-{j}")
        item(ids[-1], d - rnd.randint(0, 4), a, party=f"CLIENTE F{k}")
    move(f"mv-f{k}", d, total, f"DEPOSITO CLIENTE F{k}")
    label(f"mv-f{k}", "match", "suggested", "one_movement_many_items", ids)

# G. 12 bank charges and taxes with no item behind them.
charges = ["COMISION MANTENIMIENTO", "IVA 21% S/COMISION", "IMP LEY 25413 DEBITO", "PERCEPCION IIBB SIRCREB"]
for k in range(12):
    move(f"mv-g{k}", rnd.randint(0, 29), -(rnd.randint(100_00, 9_000_00) + k), charges[k % 4])
    label(f"mv-g{k}", "exception", "exception")

# H. 5 pairs of same amount, same day, no reference, two candidate invoices.
for k in range(5):
    a, d = amount(), rnd.randint(1, 27)
    for j in range(2):
        item(f"inv-h{k}-{j}", d - 1, a, party=f"CLIENTE H{k}{j}")
        move(f"mv-h{k}-{j}", d, a, "TRANSF RECIBIDA")
        label(f"mv-h{k}-{j}", "pending", "suggested")

# I. 5 credits nobody expected.
for k in range(5):
    move(f"mv-i{k}", rnd.randint(0, 29), amount(), "CREDITO VARIOS")
    label(f"mv-i{k}", "exception", "exception")

# J. 10 partial payments that name the invoice but pay part of it.
for k in range(10):
    a, d = amount(), rnd.randint(1, 27)
    inv = f"0005-{3000 + k:08d}"
    item(f"inv-j{k}", d - rnd.randint(1, 20), a, [inv])
    move(f"mv-j{k}", d, a // 2 + 7, f"TRANSF RECIBIDA PAGO A CUENTA FC {inv}")
    label(f"mv-j{k}", "pending", "suggested")

# Open invoices nobody paid this month (distractors).
for k in range(15):
    item(f"inv-open{k}", rnd.randint(0, 29), amount(), [f"0007-{4000 + k:08d}"])

assert len(moves) == 200, len(moves)
rnd.shuffle(moves)
rnd.shuffle(items)
with open("suite_sintetica.json", "w") as f:
    json.dump({"note": "synthetic, labeled by construction; see gen_suite.py",
               "movements": moves, "items": items, "labels": labels}, f, indent=1, ensure_ascii=False)
    f.write("\n")
