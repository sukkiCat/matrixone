#include "textflag.h"

// func iSumAvx(x []int64) int64
TEXT ·iSumAvx(SB), NOSPLIT, $0-32
	MOVQ   x_base+0(FP), AX
	MOVQ   x_len+8(FP), CX
	VXORPD Y0, Y0, Y0
	VXORPD Y1, Y1, Y1
	VXORPD Y2, Y2, Y2
	VXORPD Y3, Y3, Y3
	VXORPD Y4, Y4, Y4
	VXORPD Y5, Y5, Y5
	VXORPD Y6, Y6, Y6
	VXORPD Y7, Y7, Y7
	VXORPD Y8, Y8, Y8

loop:
	CMPQ   CX, $0x00000020
	JL     tailloop
	VPADDQ (AX), Y1, Y1
	VPADDQ 32(AX), Y2, Y2
	VPADDQ 64(AX), Y3, Y3
	VPADDQ 96(AX), Y4, Y4
	VPADDQ 128(AX), Y5, Y5
	VPADDQ 160(AX), Y6, Y6
	VPADDQ 192(AX), Y7, Y7
	VPADDQ 224(AX), Y8, Y8
	ADDQ   $0x00000100, AX
	SUBQ   $0x00000020, CX
	JMP    loop

tailloop:
	CMPQ   CX, $0x00000004
	JL     done
	VPADDQ (AX), Y0, Y0
	ADDQ   $0x00000020, AX
	SUBQ   $0x00000004, CX
	JMP    tailloop

done:
	VPADDQ       Y1, Y0, Y0
	VPADDQ       Y2, Y0, Y0
	VPADDQ       Y3, Y0, Y0
	VPADDQ       Y4, Y0, Y0
	VPADDQ       Y5, Y0, Y0
	VPADDQ       Y6, Y0, Y0
	VPADDQ       Y7, Y0, Y0
	VPADDQ       Y8, Y0, Y0
	VXORPD       X1, X1, X1
	VXORPD       X2, X2, X2
	VEXTRACTI128 $0x00, Y0, X1
	VEXTRACTI128 $0x01, Y0, X2
	VPADDQ       X1, X2, X2
	VPSHUFD      $0x4e, X2, X1
	VPADDD       X1, X2, X2
	MOVQ         X2, ret+24(FP)
	RET