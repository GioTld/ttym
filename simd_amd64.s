//go:build amd64

#include "textflag.h"

// DATA masks for PSHUFB
// Low 2 pixels mask: words [R0, G0, B0, 0, R1, G1, B1, 0]
DATA ·maskLowPx<>+0x00(SB)/8, $0x8080800280018000
DATA ·maskLowPx<>+0x08(SB)/8, $0x8080800580048003
GLOBL ·maskLowPx<>(SB), RODATA|NOPTR, $16

// High 2 pixels mask: words [R2, G2, B2, 0, R3, G3, B3, 0]
DATA ·maskHighPx<>+0x00(SB)/8, $0x8080800880078006
DATA ·maskHighPx<>+0x08(SB)/8, $0x8080800b800a8009
GLOBL ·maskHighPx<>(SB), RODATA|NOPTR, $16

// TEXT ·clampDitherRGBAsm(SB), NOSPLIT, $0-16
// func clampDitherRGBAsm(r, g, b uint8, offset int16) (uint8, uint8, uint8)
TEXT ·clampDitherRGBAsm(SB), NOSPLIT, $0-16
	MOVBLZX r_in+0(FP), AX
	MOVBLZX g_in+1(FP), BX
	MOVBLZX b_in+2(FP), CX
	MOVW offset+4(FP), DX
	SHLQ $48, DX
	SARQ $48, DX

	ADDQ DX, AX
	ADDQ DX, BX
	ADDQ DX, CX

	// Clamp AX
	TESTQ AX, AX
	JGE check_ax_hi
	XORQ AX, AX
	JMP check_bx
check_ax_hi:
	CMPQ AX, $255
	JLE check_bx
	MOVQ $255, AX

check_bx:
	TESTQ BX, BX
	JGE check_bx_hi
	XORQ BX, BX
	JMP check_cx
check_bx_hi:
	CMPQ BX, $255
	JLE check_cx
	MOVQ $255, BX

check_cx:
	TESTQ CX, CX
	JGE check_cx_hi
	XORQ CX, CX
	JMP clamp_done
check_cx_hi:
	CMPQ CX, $255
	JLE clamp_done
	MOVQ $255, CX

clamp_done:
	MOVB AX, ret0+8(FP)
	MOVB BX, ret1+9(FP)
	MOVB CX, ret2+10(FP)
	RET

// TEXT ·rgbDistBatch4Asm(SB), NOSPLIT, $0-32
// func rgbDistBatch4Asm(px *[4]RGB, c0R, c0G, c0B, c1R, c1G, c1B uint8, d0, d1 *[4]int32)
TEXT ·rgbDistBatch4Asm(SB), NOSPLIT, $0-32
	MOVQ px+0(FP), SI
	MOVQ d0+16(FP), DI
	MOVQ d1+24(FP), DX

	// Load 4 pixels (12 bytes) safely without out-of-bounds read
	MOVQ 0(SI), X0      // X0 = [R0, G0, B0, R1, G1, B1, R2, G2]
	MOVD 8(SI), X1      // X1 = [B2, R3, G3, B3]
	PUNPCKLQDQ X1, X0   // X0 = [R0..B0, R1..B1, R2..B2, R3..B3]

	// Unpack pixels into 16-bit words
	// Low pixels (0 and 1)
	MOVOU ·maskLowPx<>(SB), X2
	MOVOU X0, X3
	PSHUFB X2, X3       // X3 = [R0, G0, B0, 0, R1, G1, B1, 0] as int16 words

	// High pixels (2 and 3)
	MOVOU ·maskHighPx<>(SB), X4
	MOVOU X0, X5
	PSHUFB X4, X5       // X5 = [R2, G2, B2, 0, R3, G3, B3, 0] as int16 words

	// Prepare c0 as words [c0R, c0G, c0B, 0, c0R, c0G, c0B, 0]
	PXOR X6, X6
	MOVBLZX c0R+8(FP), AX
	MOVBLZX c0G+9(FP), BX
	MOVBLZX c0B+10(FP), CX
	PINSRW $0, AX, X6
	PINSRW $1, BX, X6
	PINSRW $2, CX, X6
	PINSRW $4, AX, X6
	PINSRW $5, BX, X6
	PINSRW $6, CX, X6

	// Prepare c1 as words [c1R, c1G, c1B, 0, c1R, c1G, c1B, 0]
	PXOR X7, X7
	MOVBLZX c1R+11(FP), AX
	MOVBLZX c1G+12(FP), BX
	MOVBLZX c1B+13(FP), CX
	PINSRW $0, AX, X7
	PINSRW $1, BX, X7
	PINSRW $2, CX, X7
	PINSRW $4, AX, X7
	PINSRW $5, BX, X7
	PINSRW $6, CX, X7

	// Compute distances to c0
	// Low: d0_low = (X3 - X6)^2
	MOVOU X3, X8
	PSUBW X6, X8
	PMADDWL X8, X8      // [dR0^2+dG0^2, dB0^2, dR1^2+dG1^2, dB1^2]

	PSHUFL $0xb1, X8, X9
	PADDL X9, X8        // Lane 0: dist0, Lane 2: dist1

	// High: d0_high = (X5 - X6)^2
	MOVOU X5, X10
	PSUBW X6, X10
	PMADDWL X10, X10    // [dR2^2+dG2^2, dB2^2, dR3^2+dG3^2, dB3^2]

	PSHUFL $0xb1, X10, X11
	PADDL X11, X10      // Lane 0: dist2, Lane 2: dist3

	// Combine into [dist0, dist1, dist2, dist3]
	SHUFPS $0x88, X8, X8
	SHUFPS $0x88, X10, X10
	MOVLHPS X10, X8
	MOVOU X8, 0(DI)     // Store d0

	// Compute distances to c1
	// Low: d1_low = (X3 - X7)^2
	MOVOU X3, X8
	PSUBW X7, X8
	PMADDWL X8, X8

	PSHUFL $0xb1, X8, X9
	PADDL X9, X8

	// High: d1_high = (X5 - X7)^2
	MOVOU X5, X10
	PSUBW X7, X10
	PMADDWL X10, X10

	PSHUFL $0xb1, X10, X11
	PADDL X11, X10

	// Combine into [dist0, dist1, dist2, dist3]
	SHUFPS $0x88, X8, X8
	SHUFPS $0x88, X10, X10
	MOVLHPS X10, X8
	MOVOU X8, 0(DX)     // Store d1

	RET

// TEXT ·batchOklabDistancesAsm(SB), NOSPLIT, $0-64
// func batchOklabDistancesAsm(targetL, targetA, targetB float64, pL, pA, pB, out *float64, n int)
TEXT ·batchOklabDistancesAsm(SB), NOSPLIT, $0-64
	MOVSD targetL+0(FP), X0
	MOVSD targetA+8(FP), X1
	MOVSD targetB+16(FP), X2
	MOVQ pL+24(FP), R8
	MOVQ pA+32(FP), R9
	MOVQ pB+40(FP), R10
	MOVQ out+48(FP), R11
	MOVQ n+56(FP), CX

	XORQ AX, AX // index i = 0

oklab_loop:
	CMPQ AX, CX
	JGE oklab_done

	// dL = targetL - pL[i]
	MOVSD X0, X3
	SUBSD (R8)(AX*8), X3
	MULSD X3, X3 // dL^2

	// da = targetA - pA[i]
	MOVSD X1, X4
	SUBSD (R9)(AX*8), X4
	MULSD X4, X4 // da^2
	ADDSD X4, X3 // dL^2 + da^2

	// db = targetB - pB[i]
	MOVSD X2, X5
	SUBSD (R10)(AX*8), X5
	MULSD X5, X5 // db^2
	ADDSD X5, X3 // dL^2 + da^2 + db^2

	MOVSD X3, (R11)(AX*8)

	INCQ AX
	JMP oklab_loop

oklab_done:
	RET
