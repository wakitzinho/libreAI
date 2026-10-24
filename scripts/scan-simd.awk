# Scan `go tool objdump` output for SIMD instructions that are not valid on
# the x86-64-v1 baseline.
#
# Usage:
#   awk -v mode=avx -f scripts/scan-simd.awk disassembly.txt
#   awk -v mode=fma -f scripts/scan-simd.awk disassembly.txt
#
# Output is one line per match: TEXT symbol, tab, original disassembly line.
# The exit status is 1 when at least one match is found and 0 otherwise.
#
# The mnemonic is taken from the instruction field, not from the whole line,
# so symbol names such as `validateBaseURL` or `modernc.org/libc.VaList` are
# not mistaken for instructions. Go's Plan 9 syntax uses `V*` for AVX/AVX2/
# AVX-512, `K*` for AVX-512 mask instructions, and Y0-Y15/Z0-Z31 for the
# wider vector registers.

function is_hex(s) {
	return s ~ /^[0-9a-f]+$/
}

function is_simd(mnemonic, instruction) {
	if (mnemonic ~ /^V(ERR|ERW)$/)
		return 0
	if (mnemonic ~ /^[VK]/)
		return 1
	if (instruction ~ /(^|[^A-Za-z0-9_.])[YZ][0-9]+/)
		return 1
	return 0
}

function is_fma(mnemonic) {
	return mnemonic ~ /^VF(N?M(ADD|SUB))/
}

BEGIN {
	found = 0
	symbol = "?"
}

/^TEXT / {
	n = split($0, header, /[ \t]+/)
	if (n >= 2) {
		symbol = header[2]
		sub(/\(SB\)$/, "", symbol)
	}
	next
}

{
	# Instruction lines contain an address, a hex byte sequence, then the
	# mnemonic and operands. Splitting on whitespace lets us skip the source
	# location and tolerate the padding tabs emitted by `go tool objdump`.
	n = split($0, fields, /[ \t]+/)
	address = 0
	for (i = 1; i <= n; i++) {
		if (fields[i] ~ /^0x[0-9a-f]+$/) {
			address = i
			break
		}
	}
	if (!address)
		next

	i = address + 1
	while (i <= n && (fields[i] == "" || is_hex(fields[i])))
		i++
	if (i > n)
		next

	mnemonic = fields[i]
	instruction = $0

	if (mode == "avx" && is_simd(mnemonic, instruction)) {
		print symbol "\t" instruction
		found++
	} else if (mode == "fma" && is_fma(mnemonic)) {
		print symbol "\t" instruction
		found++
	}
}

END {
	exit(found > 0 ? 1 : 0)
}
