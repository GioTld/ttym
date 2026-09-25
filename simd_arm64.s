//go:build arm64

#include "textflag.h"

// TEXT ·clampDitherRGBAsm(SB), NOSPLIT, $0-16
// func clampDitherRGBAsm(r, g, b uint8, offset int16) (uint8, uint8, uint8)
TEXT ·clampDitherRGBAsm(SB), NOSPLIT, $0-16
	MOVBU r_in+0(FP), R0
	MOVBU g_in+1(FP), R1
	MOVBU b_in+2(FP), R2
	MOVH offset+4(FP), R3

	ADD R3, R0
	ADD R3, R1
	ADD R3, R2

	// Clamp R0 to [0, 255]
	CMP $0, R0
	CSEL LT, ZR, R0, R0
	CMP $255, R0
	MOVD $255, R4
	CSEL GT, R4, R0, R0

	// Clamp R1 to [0, 255]
	CMP $0, R1
	CSEL LT, ZR, R1, R1
	CMP $255, R1
	CSEL GT, R4, R1, R1

	// Clamp R2 to [0, 255]
	CMP $0, R2
	CSEL LT, ZR, R2, R2
	CMP $255, R2
	CSEL GT, R4, R2, R2

	MOVB R0, ret0+8(FP)
	MOVB R1, ret1+9(FP)
	MOVB R2, ret2+10(FP)
	RET

// TEXT ·rgbDistBatch4Asm(SB), NOSPLIT, $0-32
// func rgbDistBatch4Asm(px *[4]RGB, c0R, c0G, c0B, c1R, c1G, c1B uint8, d0, d1 *[4]int32)
TEXT ·rgbDistBatch4Asm(SB), NOSPLIT, $0-32
	MOVD px+0(FP), R0
	MOVBU c0R+8(FP), R1
	MOVBU c0G+9(FP), R2
	MOVBU c0B+10(FP), R3
	MOVBU c1R+11(FP), R4
	MOVBU c1G+12(FP), R5
	MOVBU c1B+13(FP), R6
	MOVD d0+16(FP), R7
	MOVD d1+24(FP), R8

	// Pixel 0
	MOVBU 0(R0), R9
	MOVBU 1(R0), R10
	MOVBU 2(R0), R11

	// d0[0]
	SUB R1, R9, R12
	MUL R12, R12, R12
	SUB R2, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R3, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 0(R7)

	// d1[0]
	SUB R4, R9, R12
	MUL R12, R12, R12
	SUB R5, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R6, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 0(R8)

	// Pixel 1
	MOVBU 3(R0), R9
	MOVBU 4(R0), R10
	MOVBU 5(R0), R11

	// d0[1]
	SUB R1, R9, R12
	MUL R12, R12, R12
	SUB R2, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R3, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 4(R7)

	// d1[1]
	SUB R4, R9, R12
	MUL R12, R12, R12
	SUB R5, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R6, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 4(R8)

	// Pixel 2
	MOVBU 6(R0), R9
	MOVBU 7(R0), R10
	MOVBU 8(R0), R11

	// d0[2]
	SUB R1, R9, R12
	MUL R12, R12, R12
	SUB R2, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R3, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 8(R7)

	// d1[2]
	SUB R4, R9, R12
	MUL R12, R12, R12
	SUB R5, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R6, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 8(R8)

	// Pixel 3
	MOVBU 9(R0), R9
	MOVBU 10(R0), R10
	MOVBU 11(R0), R11

	// d0[3]
	SUB R1, R9, R12
	MUL R12, R12, R12
	SUB R2, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R3, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 12(R7)

	// d1[3]
	SUB R4, R9, R12
	MUL R12, R12, R12
	SUB R5, R10, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	SUB R6, R11, R13
	MUL R13, R13, R13
	ADD R13, R12, R12
	MOVW R12, 12(R8)

	RET

// TEXT ·batchOklabDistancesAsm(SB), NOSPLIT, $0-64
// func batchOklabDistancesAsm(targetL, targetA, targetB float64, pL, pA, pB, out *float64, n int)
TEXT ·batchOklabDistancesAsm(SB), NOSPLIT, $0-64
	FMOVD targetL+0(FP), F0
	FMOVD targetA+8(FP), F1
	FMOVD targetB+16(FP), F2
	MOVD pL+24(FP), R0
	MOVD pA+32(FP), R1
	MOVD pB+40(FP), R2
	MOVD out+48(FP), R3
	MOVD n+56(FP), R4

	MOVD $0, R5 // index i = 0

oklab_loop:
	CMP R4, R5
	BGE oklab_done

	// dL = targetL - pL[i]
	FMOVD (R0)(R5<<3), F3
	FSUBD F3, F0, F4
	FMULD F4, F4, F4

	// da = targetA - pA[i]
	FMOVD (R1)(R5<<3), F5
	FSUBD F5, F1, F6
	FMULD F6, F6, F6
	FADDD F6, F4, F4

	// db = targetB - pB[i]
	FMOVD (R2)(R5<<3), F7
	FSUBD F7, F2, F8
	FMULD F8, F8, F8
	FADDD F8, F4, F4

	FMOVD F4, (R3)(R5<<3)

	ADD $1, R5
	B oklab_loop

oklab_done:
	RET
